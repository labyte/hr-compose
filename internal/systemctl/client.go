// Package systemctl 封装 systemctl / journalctl 的系统调用，接口化以便测试注入 fake。
package systemctl

import (
	"fmt"
	"os/exec"
	"strings"
)

// Client 是 systemctl 操作的抽象，便于在单测中用 fake 替换。
type Client interface {
	Enable(unit string) error
	Disable(unit string) error
	Start(unit string) error
	Stop(unit string) error
	Restart(unit string) error
	DaemonReload() error
	Show(unit string) (map[string]string, error)
}

// Real 是真实调用 systemctl 的实现。
type Real struct{}

// New 返回真实客户端。
func New() Client { return &Real{} }

func (c *Real) Enable(unit string) error  { return run("systemctl", "enable", unit) }
func (c *Real) Disable(unit string) error { return run("systemctl", "disable", unit) }
func (c *Real) Start(unit string) error   { return run("systemctl", "start", unit) }
func (c *Real) Stop(unit string) error    { return run("systemctl", "stop", unit) }
func (c *Real) Restart(unit string) error { return run("systemctl", "restart", unit) }
func (c *Real) DaemonReload() error       { return run("systemctl", "daemon-reload") }

// Show 读取单个 unit 的状态字段（ActiveState / SubState / MainPID / MemoryCurrent 等）。
// 使用 systemctl show 文本输出，兼容老版本 systemd（不依赖 --output=json）。
func (c *Real) Show(unit string) (map[string]string, error) {
	out, err := exec.Command("systemctl", "show", unit, "--no-pager").Output()
	if err != nil {
		return nil, err
	}
	fields := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			fields[k] = v
		}
	}
	return fields, nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
