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

// Up 生成、enable 并按依赖顺序 start 全部服务。
func (e *Engine) Up() error {
	for _, name := range e.order() {
		g, err := unit.Generate(name, e.cfg.Services[name])
		if err != nil {
			return err
		}
		if err := writeIfChanged(filepath.Join(UnitDir, g.UnitPath), g.Content); err != nil {
			return err
		}
		if err := e.sys.DaemonReload(); err != nil {
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
func (e *Engine) Down() error {
	names := e.order()
	for i := len(names) - 1; i >= 0; i-- {
		name := names[i]
		u := name + ".service"
		_ = e.sys.Stop(u)    // 未运行时报错可忽略
		_ = e.sys.Disable(u) // 未 enable 时报错可忽略
		if err := removeIfManaged(filepath.Join(UnitDir, u)); err != nil {
			return err
		}
		fmt.Printf("down %s OK\n", name)
	}
	return e.sys.DaemonReload()
}

// writeIfChanged 仅在内容变化时写入 unit 文件，保证 up 幂等。
func writeIfChanged(path, content string) error {
	if b, err := os.ReadFile(path); err == nil && string(b) == content {
		return nil
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
func (e *Engine) order() []string {
	var out []string
	visited := make(map[string]bool)
	stack := make(map[string]bool)
	var visit func(string)
	visit = func(name string) {
		if visited[name] || stack[name] {
			return
		}
		stack[name] = true
		for _, dep := range e.cfg.Services[name].DependsOn {
			visit(dep)
		}
		stack[name] = false
		visited[name] = true
		out = append(out, name)
	}
	names := make([]string, 0, len(e.cfg.Services))
	for n := range e.cfg.Services {
		names = append(names, n)
	}
	sort.Strings(names) // 稳定输出顺序
	for _, n := range names {
		visit(n)
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
