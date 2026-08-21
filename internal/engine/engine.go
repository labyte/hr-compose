// Package engine 执行各命令动作，持有编排配置与 systemctl 客户端。
package engine

import (
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

// Up 生成、enable 并按依赖顺序 start 全部服务。
func (e *Engine) Up() error {
	names := e.order()

	// 第一趟：生成并写入全部 unit（幂等，仅在内容变化时重写），期间不 reload
	for _, name := range names {
		g, err := unit.Generate(name, e.cfg.Services[name], e.cfg.Name)
		if err != nil {
			return err
		}
		if err := writeIfManaged(filepath.Join(UnitDir, g.UnitPath), g.Content); err != nil {
			return err
		}
	}
	// 所有 unit 就绪后统一 daemon-reload 一次（避免逐服务 reload）
	if err := e.sys.DaemonReload(); err != nil {
		return err
	}
	// 第二趟：按依赖顺序 enable + start
	for _, name := range names {
		g, err := unit.Generate(name, e.cfg.Services[name], e.cfg.Name)
		if err != nil {
			return err
		}
		if err := e.sys.Enable(g.UnitPath); err != nil {
			return fmt.Errorf("enable %s: %w", g.UnitPath, err)
		}
		if err := e.sys.Start(g.UnitPath); err != nil {
			return fmt.Errorf("启动 %s: %w", name, err)
		}
		fmt.Printf("up %s OK\n", name)
	}
	return nil
}

// Down 逆序 stop、disable 并删除 unit 文件（仅删除带托管标记的文件）。
// 若编排中存在使用 journal 日志的服务，清理整个 journal（journald 不支持按 unit 删除）。
func (e *Engine) Down() error {
	names := e.order()
	for i := len(names) - 1; i >= 0; i-- {
		name := names[i]
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
	if e.anyJournal() {
		if err := e.sys.ClearJournal(); err != nil {
			return fmt.Errorf("清理 journal 日志: %w", err)
		}
		fmt.Println("已清空系统 journal 日志")
	}
	return nil
}

// anyJournal 判断编排中是否存在使用 journal 日志输出的服务。
func (e *Engine) anyJournal() bool {
	for _, svc := range e.cfg.Services {
		if svc.EffectiveStdOutput() == "journal" {
			return true
		}
	}
	return false
}

// writeIfManaged 只在目标不存在或是 hr-compose 托管文件时写入：
//   - 非托管同名文件 → 拒绝覆盖（与 down 的删除保护对称）
//   - 托管且内容一致 → 跳过（幂等）
//   - 托管且内容变化 → 覆盖
func writeIfManaged(path, content string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return os.WriteFile(path, []byte(content), 0o644)
	}
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(b), unit.ManagedMark) {
		return fmt.Errorf("拒绝覆盖 %s：已存在同名 unit 且非 hr-compose 管理（如确需托管请先删除原文件）", path)
	}
	if string(b) == content {
		return nil // 内容一致，幂等跳过
	}
	return os.WriteFile(path, []byte(content), 0o644)
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

// order 返回按 depends_on 拓扑排序的服务名（依赖者在前）。
// 无依赖时按 yml 声明顺序（ServiceOrder）输出；声明顺序不可得（直接构造的 Config）时回退按名称排序，保证输出稳定。
func (e *Engine) order() []string {
	names := make([]string, 0, len(e.cfg.Services))
	if order := e.cfg.ServiceOrder; len(order) > 0 {
		names = append(names, order...)
	} else {
		for n := range e.cfg.Services {
			names = append(names, n)
		}
		sort.Strings(names) // 稳定输出顺序
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
					if !visited[d] && !inStack[d] {
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

// resolve 把可选的 name 参数解析为服务名列表，空值表示全部。
func (e *Engine) resolve(name string) ([]string, error) {
	if name == "" {
		return e.order(), nil
	}
	if _, ok := e.cfg.Services[name]; !ok {
		return nil, fmt.Errorf("服务 %q 未在编排文件中定义", name)
	}
	return []string{name}, nil
}
