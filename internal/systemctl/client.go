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
	ShowMany(units []string) (map[string]map[string]string, error)
	ClearJournal() error
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

// showProps 是 ps 需要的属性，ShowMany 用 -p 过滤以减少输出。
// FragmentPath=实际 unit 文件路径，ExecMainStartTimestampMonotonic=主进程开机时刻（微秒，ps 计算运行时长用）。
// LoadState=unit 加载状态（not-found 表示未安装，ps 用于区分未安装与已停止）。
var showProps = []string{
	"Id", "ActiveState", "SubState", "LoadState", "UnitFileState", "MainPID", "MemoryCurrent",
	"FragmentPath", "ExecMainStartTimestampMonotonic",
}

// ShowMany 一次调用 systemctl show 读取多个 unit 的状态，按 unit 名（Id 字段）索引。
// 部分 unit 未加载时 systemctl 退出码非 0 但仍输出其余 unit 的块，此处解析可用部分；
// 仅当 systemctl 完全不可用（无任何输出）时返回错误。
func (c *Real) ShowMany(units []string) (map[string]map[string]string, error) {
	if len(units) == 0 {
		return map[string]map[string]string{}, nil
	}
	args := []string{"show", "--no-pager"}
	for _, p := range showProps {
		args = append(args, "-p", p)
	}
	args = append(args, units...)
	out, err := exec.Command("systemctl", args...).Output()

	result := make(map[string]map[string]string)
	for _, block := range strings.Split(string(out), "\n\n") {
		fields := make(map[string]string)
		for _, line := range strings.Split(block, "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				fields[k] = v
			}
		}
		if id := fields["Id"]; id != "" {
			result[id] = fields
		}
	}
	if err != nil && len(result) == 0 {
		return nil, err
	}
	return result, nil
}

// ClearJournal 清空 journal 日志：先 rotate 封存当前文件，再按体积归零。
// 注意：journald 不支持按 unit 删除，此处清空的是整个系统 journal。
func (c *Real) ClearJournal() error {
	if err := run("journalctl", "--rotate"); err != nil {
		return err
	}
	return run("journalctl", "--vacuum-size=1")
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
