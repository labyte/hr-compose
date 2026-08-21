package engine

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// stdout 可被测试覆盖以捕获输出（同 UnitDir 变量的模式）。
var stdout io.Writer = os.Stdout

// uptimeFile 是系统开机秒数来源（/proc/uptime 第一字段），变量形式便于测试覆盖。
var uptimeFile = "/proc/uptime"

// Ps 遍历 yml 服务，逐个读取 systemd 状态并格式化输出。
// 列：NAME / STATUS / ENABLED / PID / MEMORY / UPTIME / DESCRIPTION，终端下状态列着色。
// STATUS=ActiveState、SubState 与 LoadState 合并（mergedState），UPTIME=主进程运行时长（可判断是否重启过），
// ENABLED=UnitFileState（是否开机启动）。unit 实际文件路径见 config 命令预览。
func (e *Engine) Ps() error {
	t := table.NewWriter()
	t.SetOutputMirror(stdout)
	t.AppendHeader(table.Row{"NAME", "STATUS", "ENABLED", "PID", "MEMORY", "UPTIME", "DESCRIPTION"})
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 8, WidthMax: 40}, // 描述列限宽（默认 WidthMaxEnforcer=text.WrapText 换行）
	})
	t.SetStyle(table.StyleLight)

	names := e.order()
	unitNames := make([]string, len(names))
	for i, n := range names {
		unitNames[i] = n + ".service"
	}
	// 一次批量查询所有 unit，减少进程启动
	all, err := e.sys.ShowMany(unitNames)
	if err != nil {
		all = map[string]map[string]string{}
	}
	// 批量结果中缺失的 unit（未加载）回退逐服务查询
	for _, n := range names {
		u := n + ".service"
		if _, ok := all[u]; ok {
			continue
		}
		if f, serr := e.sys.Show(u); serr == nil {
			all[u] = f
		}
	}

	// 开机时长只读一次，供全部服务的 UPTIME 列复用
	boot, bootErr := readUptime()
	on := colorsOn()
	for _, name := range names {
		svc := e.cfg.Services[name]
		fields, ok := all[name+".service"]
		if !ok {
			// Show 完全失败（如 systemctl 不可用/无任何输出）时按空状态展示；
			// 未安装的 unit 在正常 systemd 下能取到 LoadState=not-found，会走下方 not-found 分支
			t.AppendRow(table.Row{name, "-", "-", "-", "-", "-", "-", valueOrDash(svc.Description)})
			continue
		}
		var colors text.Colors
		if on {
			colors = stateColors(fields["ActiveState"])
		}
		up := "-"
		if bootErr == nil && runningStates[fields["ActiveState"]] {
			if d, ok := uptimeSince(fields["ExecMainStartTimestampMonotonic"], boot); ok {
				up = formatUptime(d)
			}
		}
		// go-pretty 计算列宽时会剥离 ANSI 转义，可安全用 Sprint 生成的彩色字符串作为单元格
		t.AppendRow(table.Row{
			name,
			colors.Sprint(mergedState(fields["ActiveState"], fields["SubState"], fields["LoadState"])),
			valueOrDash(fields["UnitFileState"]),
			fields["MainPID"],
			formatBytes(fields["MemoryCurrent"]),
			up,
			valueOrDash(svc.Description),
		})
	}
	t.Render()
	return nil
}

// runningStates 是主进程可能仍在运行的状态集合，仅这些状态计算 UPTIME；
// 停止（inactive）/ 失败（failed）的服务展示 "-"，避免"已运行 X"的误导。
var runningStates = map[string]bool{
	"active": true, "activating": true, "deactivating": true, "reloading": true,
}

// mergedState 把 ActiveState、SubState 与 LoadState 合并为单列状态，输出面向使用者的人话词汇：
//   - LoadState=not-found（未安装，unit 文件不存在，含 down 后）优先表达为 not-found，
//     区别于已安装但停止的 stopped
//   - 常见组合映射为单字：running（运行中）/ exited（oneshot 已完成）/ waiting（notify 未就绪）/
//     stopped（已停止）/ starting（启动中）/ restarting（自动重启中）/ stopping（停止中）/ reloading（重载中）/ failed（失败）
//   - activating 统一为 starting、deactivating 统一为 stopping（stop-sigterm 等内部细节不展示）；
//     auto-restart 是自动重启中，单独表达为 restarting（重点状态）
//   - 子状态为空或与主状态相同（如 failed/failed）时只显示主状态
//   - 罕见组合回退 "主/子" 两级展示，信息不丢失
func mergedState(active, sub, load string) string {
	switch {
	case load == "not-found":
		return "not-found"
	case sub == "" || sub == active:
		return active
	case active == "active" && sub == "running":
		return "running"
	case active == "active" && sub == "exited":
		return "exited"
	case active == "active" && sub == "waiting":
		return "waiting"
	case active == "inactive" && sub == "dead":
		return "stopped"
	case active == "deactivating":
		return "stopping"
	case active == "reloading":
		return "reloading"
	case active == "activating" && sub == "auto-restart":
		return "restarting"
	case active == "activating":
		return "starting"
	}
	return active + "/" + sub
}

// readUptime 读取系统开机秒数（/proc/uptime 第一字段），失败返回错误。
func readUptime() (string, error) {
	b, err := os.ReadFile(uptimeFile)
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return "", fmt.Errorf("uptime: 空内容")
	}
	return f[0], nil
}

// uptimeSince 用开机时长减去主进程开机时刻（ExecMainStartTimestampMonotonic，微秒），
// 得到主进程已运行时长。字段缺失/为 0（未启动过）或结果非正（时钟异常）时返回 false。
func uptimeSince(monotonic, bootSec string) (time.Duration, bool) {
	if monotonic == "" || monotonic == "0" {
		return 0, false
	}
	start, err := strconv.ParseUint(monotonic, 10, 64)
	if err != nil {
		return 0, false
	}
	boot, err := strconv.ParseFloat(bootSec, 64)
	if err != nil {
		return 0, false
	}
	// 整秒粒度相减，避免浮点纳秒在超长 uptime（数百天）下丢精度；展示本身只到秒。
	d := time.Duration(boot)*time.Second - time.Duration(start)*time.Microsecond
	if d <= 0 {
		return 0, false
	}
	return d, true
}

// formatUptime 把运行时长格式化为紧凑单位（s/m/h/d），非正数显示 "-"。
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		m, s := int(d.Minutes()), int(d.Seconds())%60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	case d < 24*time.Hour:
		h, m := int(d.Hours()), int(d.Minutes())%60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		dd, h := int(d.Hours())/24, int(d.Hours())%24
		if h == 0 {
			return fmt.Sprintf("%dd", dd)
		}
		return fmt.Sprintf("%dd%dh", dd, h)
	}
}

// valueOrDash 空值显示 "-"。
func valueOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// formatBytes 把字节数格式化为友好单位（K/M/G）；非数值或 systemd 未统计哨兵值原样处理。
func formatBytes(s string) string {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return s // 非数值（"-" 或已格式化）原样返回
	}
	// systemd 未启用内存统计时 MemoryCurrent 为 2^64-1 哨兵值
	if n >= 1<<50 {
		return "-"
	}
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fK", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
