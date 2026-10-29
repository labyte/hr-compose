// Package engine 执行各命令动作，持有编排配置与 systemctl 客户端。
package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"hr.compose/internal/config"
	"hr.compose/internal/systemctl"
	"hr.compose/internal/unit"
)

// UnitDir 是 systemd 单元文件目录。用变量而非常量，便于测试覆盖。
var UnitDir = "/etc/systemd/system"

// Engine 执行各命令动作。
type Engine struct {
	cfg *config.Config
	sys systemctl.Client
}

// New 构造引擎。
func New(cfg *config.Config, sys systemctl.Client) *Engine {
	return &Engine{cfg: cfg, sys: sys}
}

// unitName 返回服务对应的 unit 文件名：<project>-<service>.service；cfg.Name（name:）为空时不加前缀
// （生产环境 Load 必填 name，空只出现在直接构造 Config 的测试里）。
func (e *Engine) unitName(n string) string {
	return unit.Name(e.cfg.Name, n)
}

// Up 生成、enable 并按依赖顺序启动服务。
// 指定名字时自动带上其 depends_on 依赖（递归），保证依赖先安装先启动；不指定则全部。
// unit 内容变化且服务正在运行时改用 restart——start 对 active 的 unit 是 no-op，
// 只 daemon-reload 不重启的话新配置对运行中的进程不生效。
// 收尾复核启动结果（见 checkUp）并展示状态表：start 返回 0 不等于进程活着，
// 没起来的服务逐条报出原因并使命令以非 0 退出，绝不谎报成功。
func (e *Engine) Up(names ...string) error {
	targets, err := e.upTargets(names)
	if err != nil {
		return err
	}

	// 第一趟：生成并写入全部 target 的 unit（幂等，仅在内容变化时重写），期间不 reload。
	// 生成产物与写入结果记下来给第二趟复用，避免重复渲染 unit。
	plan := make([]upTarget, 0, len(targets))
	units := make([]string, 0, len(targets))
	for _, name := range targets {
		g, err := unit.Generate(name, e.cfg.Services[name], e.cfg.Name)
		if err != nil {
			return err
		}
		action, err := writeIfManaged(filepath.Join(UnitDir, g.UnitPath), g.Content)
		if err != nil {
			return err
		}
		plan = append(plan, upTarget{name: name, unitPath: g.UnitPath, write: action})
		units = append(units, g.UnitPath)
	}

	// 有 unit 被重写时，取"写入前的运行状态"用于判断该 restart 还是 start。
	// 只改文件不影响运行中的进程，daemon-reload 之前取与之后取对 ActiveState 等价，故放在 reload 前。
	if anyReplaced(plan) {
		if states, err := e.sys.ShowMany(units); err == nil {
			for i := range plan {
				plan[i].wasRunning = stateIsRunning(states[plan[i].unitPath])
			}
		}
	}

	// 所有 unit 就绪后统一 daemon-reload 一次（避免逐服务 reload）
	if err := e.sys.DaemonReload(); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	// 第二趟：按依赖顺序 enable + 启动。单个服务失败不中断其余服务（依赖不满足的那个由 systemd
	// 报错，其他服务照常安装启动），但结束时汇总报错，绝不把部分成功说成成功。
	var errs []error
	var failed []string
	for _, t := range plan {
		if err := e.sys.Enable(t.unitPath); err != nil {
			errs = append(errs, fmt.Errorf("enable %s: %w", t.unitPath, err))
			failed = append(failed, t.name)
			continue
		}
		verb, act := "启动", e.sys.Start
		if t.write == unitReplaced && t.wasRunning {
			verb, act = "重启", e.sys.Restart
		}
		if err := act(t.unitPath); err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", verb, t.name, err))
			failed = append(failed, t.name)
			continue
		}
		// 只说"已提交"：start 返回 0 是 systemd 受理了任务，进程是否真活着由下面的复核定论。
		fmt.Printf("up %s 已%s\n", t.name, verb)
	}
	if len(failed) > 0 {
		errs = append(errs, fmt.Errorf("%d 个服务未成功: %s", len(failed), strings.Join(failed, "、")))
	}
	// 收尾：复核启动结果并展示本次目标服务的状态。复核是必须的——真机实测 command 路径不存在时
	// `systemctl start` 仍返回 0（Restart=always 让它进入 auto-restart 循环），不复核就是谎报成功。
	// 失败的服务在表里直接呈现 failed/restarting，用户不必读 systemctl 的长报错也能看出哪些没起来
	// （部分失败时同样展示）；start 本身已报错的服务原因明确，不重复下结论。
	justDone := make([]string, 0, len(plan))
	for _, t := range plan {
		justDone = append(justDone, t.name)
	}
	states, notUp, cerr := e.checkUp(justDone)
	if cerr != nil {
		errs = append(errs, fmt.Errorf("复核服务状态: %w", cerr))
		states = map[string]map[string]string{}
	}
	skip := make(map[string]bool, len(failed))
	for _, n := range failed {
		skip[n] = true
	}
	for _, n := range justDone { // 按拓扑序汇总，错误顺序稳定
		if reason, ok := notUp[n]; ok && !skip[n] {
			errs = append(errs, fmt.Errorf("%s 未能正常运行：%s", n, reason))
		}
	}
	if err := e.renderPs(justDone, states); err != nil {
		errs = append(errs, fmt.Errorf("展示服务状态: %w", err))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// unitWrite 描述 writeIfManaged 的落盘结果，供 Up 决定后续动作。
type unitWrite int

const (
	unitUnchanged unitWrite = iota // 内容一致，未写盘
	unitCreated                    // 首次安装：文件原本不存在
	unitReplaced                   // 托管文件内容变化，已重写
)

// upTarget 是 Up 第二趟处理的一个服务：unit 文件名、落盘结果、写入前是否在运行。
type upTarget struct {
	name       string
	unitPath   string
	write      unitWrite
	wasRunning bool
}

// anyReplaced 判断本次是否有托管 unit 被重写——只有这种情况才需要查询运行状态。
func anyReplaced(plan []upTarget) bool {
	for _, t := range plan {
		if t.write == unitReplaced {
			return true
		}
	}
	return false
}

// stateIsRunning 判断 systemctl show 结果是否表示进程还在跑（activating/deactivating/reloading
// 都未落到稳定态，同样按在跑处理，因为 restart 才能把新配置带进进程）。
func stateIsRunning(fields map[string]string) bool {
	return runningStates[fields["ActiveState"]]
}

// upTargets 解析 up 的目标集并补齐依赖闭包：
//   - 无参数 → 全部服务（拓扑序，与旧行为一致）
//   - 有参数 → 校验名字后，取这些服务及其 depends_on 的传递闭包，再拓扑排序，
//     保证首次 `up <服务>` 时依赖也一并安装并先启动（docker-compose 语义）。
func (e *Engine) upTargets(names []string) ([]string, error) {
	if len(names) == 0 {
		return e.order(e.allNames()), nil
	}
	if _, err := e.resolve(names...); err != nil {
		return nil, err // 未定义的名字直接报错
	}
	inSet := make(map[string]bool)
	var visit func(string)
	visit = func(n string) {
		if inSet[n] {
			return
		}
		inSet[n] = true
		for _, d := range e.cfg.Services[n].DependsOn {
			visit(d)
		}
	}
	for _, n := range names {
		visit(n)
	}
	subset := make([]string, 0, len(inSet))
	for _, n := range e.allNames() { // 声明顺序为基准，保证输出稳定
		if inSet[n] {
			subset = append(subset, n)
		}
	}
	return e.order(subset), nil
}

// Down 逆序 stop、disable 并删除指定服务的 unit 文件（仅删除带托管标记的文件），
// 只清理指定服务、不连带其依赖；不指定则全部。
// 若目标服务中存在使用 journal 日志的服务，清理整个 journal（journald 不支持按 unit 删除）。
func (e *Engine) Down(names ...string) error {
	targets, err := e.resolve(names...)
	if err != nil {
		return err
	}
	for i := len(targets) - 1; i >= 0; i-- {
		name := targets[i]
		u := e.unitName(name)
		_ = e.sys.Stop(u)    // 未运行时报错可忽略
		_ = e.sys.Disable(u) // 未 enable 时报错可忽略
		if err := removeIfManaged(filepath.Join(UnitDir, u)); err != nil {
			return err
		}
		fmt.Printf("down %s OK\n", name)
	}
	if err := e.sys.DaemonReload(); err != nil {
		return err
	}
	if e.anyJournal(targets) {
		if err := e.sys.ClearJournal(); err != nil {
			return fmt.Errorf("清理 journal 日志: %w", err)
		}
		fmt.Println("已清空系统 journal 日志")
	}
	return nil
}

// anyJournal 判断目标服务中是否存在使用 journal 日志输出的服务。
func (e *Engine) anyJournal(names []string) bool {
	for _, n := range names {
		if e.cfg.Services[n].EffectiveStdOutput() == "journal" {
			return true
		}
	}
	return false
}

// writeIfManaged 只在目标不存在或是 hr-compose 托管文件时写入，并返回落盘结果供 Up 决策：
//   - 非托管同名文件 → 拒绝覆盖（与 down 的删除保护对称）
//   - 托管且内容一致 → 跳过（幂等），返回 unitUnchanged
//   - 托管且内容变化 → 覆盖，返回 unitReplaced
//   - 文件原本不存在 → 首次安装，返回 unitCreated
func writeIfManaged(path, content string) (unitWrite, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return unitUnchanged, err
		}
		return unitCreated, nil
	}
	if err != nil {
		return unitUnchanged, err
	}
	if !strings.HasPrefix(string(b), unit.ManagedMark) {
		return unitUnchanged, fmt.Errorf("拒绝覆盖 %s：已存在同名 unit 且非 hr-compose 管理（如确需托管请先删除原文件）", path)
	}
	if string(b) == content {
		return unitUnchanged, nil // 内容一致，幂等跳过
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return unitUnchanged, err
	}
	return unitReplaced, nil
}

// removeIfManaged 只删除带 hr-compose 托管标记的文件，防止误删同名系统服务。
func removeIfManaged(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(b), unit.ManagedMark) {
		return fmt.Errorf("拒绝删除 %s：文件非 hr-compose 管理", path)
	}
	return os.Remove(path)
}

// allNames 返回全部服务名的稳定基准顺序：yml 声明顺序（ServiceOrder）；
// 声明顺序不可得（直接构造的 Config）时回退按名称排序，保证输出稳定。
func (e *Engine) allNames() []string {
	names := make([]string, 0, len(e.cfg.Services))
	if order := e.cfg.ServiceOrder; len(order) > 0 {
		names = append(names, order...)
	} else {
		for n := range e.cfg.Services {
			names = append(names, n)
		}
		sort.Strings(names) // 稳定输出顺序
	}
	return names
}

// order 对给定服务名做 depends_on 拓扑排序（依赖者在前），子集外的依赖忽略（视为已满足）。
// 输入顺序作为无依赖服务的基准顺序。
func (e *Engine) order(names []string) []string {
	inSet := make(map[string]bool, len(names))
	for _, n := range names {
		inSet[n] = true
	}

	var out []string
	visited := make(map[string]bool)
	inStack := make(map[string]bool)
	for _, start := range names {
		if visited[start] {
			continue
		}
		stack := []string{start}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			switch {
			case visited[n]:
				stack = stack[:len(stack)-1]
			case inStack[n]:
				// 依赖已全部处理完，完成该节点
				inStack[n] = false
				visited[n] = true
				out = append(out, n)
				stack = stack[:len(stack)-1]
			default:
				// 首次遇到：压入未处理依赖（逆序压栈保持与递归一致的遍历顺序）
				inStack[n] = true
				deps := e.cfg.Services[n].DependsOn
				pushed := false
				for i := len(deps) - 1; i >= 0; i-- {
					d := deps[i]
					if inSet[d] && !visited[d] && !inStack[d] {
						stack = append(stack, d)
						pushed = true
					}
				}
				if !pushed {
					// 无未处理依赖，立即完成
					inStack[n] = false
					visited[n] = true
					out = append(out, n)
					stack = stack[:len(stack)-1]
				}
			}
		}
	}
	return out
}

// resolve 把可选的服务名解析为有序目标列表：
//   - 无参数 → 全部服务（拓扑序，依赖者在前）
//   - 有参数 → 校验每个名字已定义（未定义一次全部列出）、去重保序、在子集内拓扑排序
func (e *Engine) resolve(names ...string) ([]string, error) {
	if len(names) == 0 {
		return e.order(e.allNames()), nil
	}
	var missing []string
	seen := make(map[string]bool, len(names))
	subset := make([]string, 0, len(names))
	for _, n := range names {
		if _, ok := e.cfg.Services[n]; !ok {
			missing = append(missing, n)
			continue
		}
		if !seen[n] {
			seen[n] = true
			subset = append(subset, n)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("服务 %q 未在编排文件中定义", strings.Join(missing, `", "`))
	}
	return e.order(subset), nil
}
