package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"

	"hr.compose/internal/config"
)

// stdout 可被测试覆盖以捕获输出（同 UnitDir 变量的模式）。
var stdout io.Writer = os.Stdout

// uptimeFile 是系统开机秒数来源（/proc/uptime 第一字段），变量形式便于测试覆盖。
var uptimeFile = "/proc/uptime"

// Ps 打印服务状态。names 为空（nil/空切片）则展示 yml 中全部服务，否则只展示指定的服务
// （未定义的名字报错，顺序按依赖拓扑）。列：NAME / STATUS / ENABLED / PID / MEMORY / UPTIME / DESCRIPTION，
// 终端下状态列着色。STATUS=ActiveState、SubState 与 LoadState 合并（mergedState），
// UPTIME=主进程运行时长（可判断是否重启过），ENABLED=UnitFileState（是否开机启动）。
// MEMORY=working set（cgroup memory.current 扣掉可回收的 inactive_file），配了 memory_max 时附 " / 上限"。
// unit 实际文件路径见 config 命令预览。
func (e *Engine) Ps(names ...string) error {
	targets, err := e.resolve(names...)
	if err != nil {
		return err
	}
	all, err := e.fetchStates(targets)
	if err != nil {
		return err
	}
	return e.renderPs(targets, all)
}

// fetchStates 批量读取给定服务的 systemd 状态；批量结果里缺失的 unit（未加载）回退逐服务查询。
// 只有 systemctl 本身完全不可用（批量与逐个都拿不到任何字段）时才返回错误。
func (e *Engine) fetchStates(names []string) (map[string]map[string]string, error) {
	units := make([]string, len(names))
	for i, n := range names {
		units[i] = e.unitName(n)
	}
	// 一次批量查询所有 unit，减少进程启动
	all, err := e.sys.ShowMany(units)
	if err != nil {
		all = map[string]map[string]string{}
	}
	got := false
	for _, n := range names {
		u := e.unitName(n)
		if f, ok := all[u]; ok {
			got = got || len(f) > 0
			continue
		}
		// 批量结果中缺失的 unit（未加载）回退逐服务查询
		if f, serr := e.sys.Show(u); serr == nil {
			all[u] = f
			got = got || len(f) > 0
		}
	}
	if !got && err != nil {
		return nil, err
	}
	return all, nil
}

// renderPs 按给定状态数据渲染状态表（names 是展示顺序）。与 fetchStates 分开，
// 便于 up 先查一次状态做启动复核、再用同一份数据画表，不重复调用 systemctl。
func (e *Engine) renderPs(names []string, all map[string]map[string]string) error {
	t := table.NewWriter()
	t.SetOutputMirror(stdout)
	t.AppendHeader(table.Row{"NAME", "STATUS", "ENABLED", "PID", "MEMORY", "UPTIME", "DESCRIPTION"})
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 7, WidthMax: 40}, // 描述列限宽（默认 WidthMaxEnforcer=text.WrapText 换行）
	})
	t.SetStyle(table.StyleLight)

	// 开机时长只读一次，供全部服务的 UPTIME 列复用
	boot, bootErr := readUptime()
	on := colorsOn()
	for _, name := range names {
		svc := e.cfg.Services[name]
		fields, ok := all[e.unitName(name)]
		if !ok {
			// Show 完全失败（如 systemctl 不可用/无任何输出）时按空状态展示；
			// 未安装的 unit 在正常 systemd 下能取到 LoadState=not-found，会走下方 not-found 分支
			t.AppendRow(table.Row{name, "-", "-", "-", "-", "-", valueOrDash(svc.Description)})
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
			pidOrDash(fields["MainPID"]),
			memoryCell(e.unitName(name), svc, fields, on),
			up,
			valueOrDash(svc.Description),
		})
	}
	t.Render()
	return nil
}

// cgroupBase 是 cgroup 挂载根目录。变量形式便于测试覆盖（同 UnitDir 的模式）。
// cgroup v2（统一层级）下 systemd 系统级 unit 的目录是 <root>/system.slice/<unit>.service/，
// cgroup v1 或混合层级下该路径不存在，读不到时内存展示自动回退 systemd 的 MemoryCurrent。
var cgroupBase = "/sys/fs/cgroup"

// memoryCell 组装 ps 的 MEMORY 单元格，口径见 workingSetBytes。
// 配置了 memory_max 时追加 " / <上限>"，并在用量逼近上限时给出警示标记/配色。
// 拿不到内存数据（unit 未安装、未启用 MemoryAccounting、systemd 输出字面量 "[not set]"、
// 老版本的 2^64-1 哨兵值）时统一显示 "-"，不把 systemd 的内部字面量透给用户。
func memoryCell(unitName string, svc *config.Service, fields map[string]string, on bool) string {
	cur, haveCur := memCurrentBytes(fields["MemoryCurrent"])
	if !haveCur {
		return "-"
	}
	shown := formatUintBytes(cur)
	// 读得到 cgroup 原生接口时改用 working set（剔除可回收页缓存）
	if stat, ok := readMemoryStat(filepath.Join(cgroupBase, "system.slice", unitName, "memory.stat")); ok {
		if ws, ok2 := workingSetBytes(cur, stat); ok2 {
			shown = formatUintBytes(ws)
		}
	}
	if svc.MemoryMax == "" {
		return shown
	}
	max, ok := parseByteSize(svc.MemoryMax)
	if !ok {
		return shown + " / " + svc.MemoryMax // infinity 等解析不了的上限原样展示
	}
	shown += " / " + formatUintBytes(max)
	// 上限解析为 0（memory_max: "0" / "0K" 一类）时不计算占用比：除数为 0 会让只读的 ps 直接 panic。
	// 0 上限本身是无效配置（systemd 语义等于不允许占用任何内存），展示上限即可，警示交给 up 的启动复核。
	if max == 0 {
		return shown
	}
	// 上限占用比用含页缓存的 current 计算：systemd 判超限/OOM 用的就是这个数，
	// 与主数（working set，已剔除可回收缓存）口径不同，故只作为"离上限还有多远"的比值。
	if mark, colors := memoryWarn(cur * 100 / max); mark != "" {
		if !on {
			colors = nil // 颜色关闭时只留 ASCII 标记（管道/重定向场景）
		}
		shown = colors.Sprint(shown + mark)
	}
	return shown
}

// pidOrDash 主进程 PID：未运行/未启动时 systemd 给 0 或空，展示为 "-" 而不是无意义的 0。
func pidOrDash(pid string) string {
	if pid == "" || pid == "0" {
		return "-"
	}
	return pid
}

// memoryWarn 按内存用量比（current / memory_max，百分比）返回警示标记与配色：
//   - >=90% 红色 "!!"：离被内核杀掉只剩一点余量
//   - 80%~90% 黄色 "!"：偏高，值得看一眼
//   - 其余不警示，避免正常服务的输出里全是噪声
func memoryWarn(pct uint64) (string, text.Colors) {
	switch {
	case pct >= 90:
		return " !! 接近上限", text.Colors{text.FgRed}
	case pct >= 80:
		return " ! 用量偏高", text.Colors{text.FgYellow}
	}
	return "", nil
}

// workingSetBytes 按 Kubernetes working set 同口径算服务"真正需要"的内存：
// memory.current 减去 memory.stat 的 inactive_file（内核可随时回收的页缓存）。
// 业务读写磁盘文件时，页缓存会被记到第一个触碰它的 cgroup 的 memory.current 上，
// 直接用 current 会把 300M 文件缓存算进"内存占用"，且这个数还会随内核回收动作来回抖动。
func workingSetBytes(current uint64, stat map[string]uint64) (uint64, bool) {
	if stat == nil {
		return 0, false
	}
	ws := current
	if f := stat["inactive_file"]; f < ws {
		ws -= f
	}
	return ws, true
}

// readMemoryStat 解析 cgroup v2 的 memory.stat（每行 "键 数值"，数值单位字节）。
// 文件不存在或不可读时 ok=false，调用方回退 systemd 口径。
func readMemoryStat(path string) (map[string]uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	out := make(map[string]uint64, 16)
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if v, err := strconv.ParseUint(f[1], 10, 64); err == nil {
			out[f[0]] = v
		}
	}
	return out, len(out) > 0
}

// memCurrentBytes 解析 systemctl show 的 MemoryCurrent。
// 非数值（未取到）与 systemd 未启用内存统计时的哨兵值（接近 2^64）都返回 ok=false。
func memCurrentBytes(s string) (uint64, bool) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n >= 1<<50 {
		return 0, false
	}
	return n, true
}

// parseByteSize 解析 systemd 的体积取值（1024 进制，可带小数与可选空格：500M、1.5 G、2048K）。
// 认不出的写法（infinity、裸字节不合法等）返回 ok=false，由调用方原样展示。
func parseByteSize(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil || n < 0 {
		return 0, false
	}
	var mult uint64 = 1
	switch strings.ToUpper(strings.TrimSpace(s[i:])) {
	case "":
	case "K", "KI", "KIB", "KB":
		mult = 1 << 10
	case "M", "MI", "MIB", "MB":
		mult = 1 << 20
	case "G", "GI", "GIB", "GB":
		mult = 1 << 30
	case "T", "TI", "TIB", "TB":
		mult = 1 << 40
	case "P", "PI", "PIP", "PB":
		mult = 1 << 50
	default:
		return 0, false
	}
	return uint64(n * float64(mult)), true
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

// formatUintBytes 把字节数格式化为友好单位（G/M 保留一位小数，K 取整）。
func formatUintBytes(n uint64) string {
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
