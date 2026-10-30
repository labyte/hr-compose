// Package systemctl 封装 systemctl / journalctl 的系统调用，接口化以便测试注入 fake。
package systemctl

import (
	"errors"
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
// 失败时经 execErr 带上 stderr，让上层能告诉用户"为什么读不到状态"。
func (c *Real) Show(unit string) (map[string]string, error) {
	out, err := exec.Command("systemctl", "show", unit, "--no-pager").Output()
	if err != nil {
		return nil, execErr("systemctl", []string{"show", unit, "--no-pager"}, err)
	}
	fields := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			fields[k] = v
		}
	}
	return fields, nil
}

// showProps 是 ps 与 up 复核需要的属性，ShowMany 用 -p 过滤以减少输出。
// ExecMainStartTimestampMonotonic=主进程开机时刻（微秒，ps 计算运行时长用）。
// LoadState=unit 加载状态（not-found 表示未安装，ps 用于区分未安装与已停止）。
// Result/ExecMainCode/ExecMainStatus/NRestarts=进程没起来时 systemd 自己的失败口径，
// up 的启动复核用它拼出可直接指导修复的原因，不自己猜失败原因。
var showProps = []string{
	"Id", "ActiveState", "SubState", "LoadState", "UnitFileState", "MainPID", "MemoryCurrent",
	"ExecMainStartTimestampMonotonic",
	"Result", "ExecMainCode", "ExecMainStatus", "NRestarts",
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
		// 错误里只带 show 与 unit 名：-p 属性列表是内部实现细节，塞进消息只会淹没真正的原因。
		return nil, execErr("systemctl", append([]string{"show"}, units...), err)
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

// execErr 把一次失败的外部命令包装成"命令 + 原因"的错误。
// Show/ShowMany 用 Output() 只取 stdout，失败时 stderr 在 *exec.ExitError.Stderr 里；
// 直接返回原 err 的话上层只剩 "exit status 1"，用户看不出是连不上 systemd、权限不足，
// 还是 unit 不存在（ps/config 这类只读命令尤其明显，它们不会打印别的上下文）。
func execErr(name string, args []string, err error) error {
	cmdline := name
	if len(args) > 0 {
		cmdline += " " + strings.Join(args, " ")
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return fmt.Errorf("%s: %w\n%s", cmdline, err, strings.TrimSpace(string(exitErr.Stderr)))
	}
	return fmt.Errorf("%s: %w", cmdline, err)
}
