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
// 列：NAME / STATUS / ENABLED / PID / MEMORY / UPTIME / CONFIG / DESCRIPTION，终端下状态列着色。
// STATUS=ActiveState 与 SubState 合并（mergedState），UPTIME=主进程运行时长（可判断是否重启过），
// CONFIG=FragmentPath（systemd 实际加载的 unit 文件路径），ENABLED=UnitFileState（是否开机启动）。
func (e *Engine) Ps() error {
	t := table.NewWriter()
	t.SetOutputMirror(stdout)
	t.AppendHeader(table.Row{"NAME", "STATUS", "ENABLED", "PID", "MEMORY", "UPTIME", "CONFIG", "DESCRIPTION"})
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
			// unit 未加载（未启动过或已 down）时按空状态展示
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
			colors.Sprint(mergedState(fields["ActiveState"], fields["SubState"])),
			valueOrDash(fields["UnitFileState"]),
			fields["MainPID"],
			formatBytes(fields["MemoryCurrent"]),
			up,
			valueOrDash(fields["FragmentPath"]),
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

// mergedState 把 ActiveState 与 SubState 合并为单列状态，语义保持等价：
//   - 子状态为空或与主状态相同（如 failed/failed）时省略，只显示主状态
//   - 主状态下的默认子状态冗余（active 的 running、inactive 的 dead、deactivating 的 stop、reloading 的 reload），省略
//   - activating + auto-restart 是自动重启中，单独表达为 restarting（重点状态）
//   - 其余组合以 "主/子" 两级展示，信息完整不丢失
func mergedState(active, sub string) string {
	switch {
	case sub == "" || sub == active:
		return active
	case active == "active" && sub == "running":
		return active
	case active == "inactive" && sub == "dead":
		return active
	case active == "deactivating" && sub == "stop":
		return active
	case active == "reloading" && sub == "reload":
		return active
	case active == "activating" && sub == "auto-restart":
		return "restarting"
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
