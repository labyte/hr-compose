package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
// （未定义的名字报错，顺序按依赖拓扑）。列：NAME / STATUS / ENABLED / PID / MEMORY / UPTIME，
// 终端下状态列着色。STATUS=ActiveState、SubState 与 LoadState 合并（mergedState），
// UPTIME=主进程运行时长（可判断是否重启过），ENABLED=UnitFileState（是否开机启动）。
// MEMORY=匿名内存（cgroup memory.stat 的 anon，与系统监视器口径一致），配了 memory_max 时附 " / 上限"。
// unit 实际文件路径见 config 命令预览；持续刷新见 WatchPs。
func (e *Engine) Ps(names ...string) error {
	targets, err := e.resolve(names...)
	if err != nil {
		return err
	}
	all, err := e.fetchStates(targets)
	if err != nil {
		return err
	}
	_, err = e.renderPs(targets, all)
	return err
}

// DefaultWatchInterval 是 ps --watch 的默认刷新间隔（不传 --interval/-i 时的取值）。
const DefaultWatchInterval = 2 * time.Second

// watchStop 是 ps --watch 的中断信号源：收到（或 channel 关闭）即停止刷新。默认 nil，
// WatchPs 内改用 signal.NotifyContext 监听 SIGINT（Ctrl+C）/SIGTERM；测试覆盖为手动关闭的
// channel 以确定性地结束循环（真实信号在单测里不易注入）。
var watchStop <-chan struct{}

// termIsTerminal 判断 stdout 是否为终端（字符设备）。ps --watch 原位重绘用到的 ANSI 控制序列
// （光标上移 / 从光标清到屏幕末尾）只在终端下生效，管道/重定向时不塞转义码。变量形式便于测试覆盖。
var termIsTerminal = func() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// moveCursorUp 输出 ANSI 光标上移序列，让下一轮状态表回到上一张表顶原位覆盖，而不是清整屏
// （清整屏会抹掉用户此前的终端内容、留一大片空白）。仅 stdout 是终端时生效。
func moveCursorUp(n int) {
	if n <= 0 || !termIsTerminal() {
		return
	}
	fmt.Fprintf(stdout, "\x1b[%dA", n)
}

// clearToEndOfScreen 输出 ANSI 从光标到屏幕末尾的清除序列（\x1b[0J）。原位重绘时用于擦掉
// 上一轮比本轮多出的行（如终端变窄导致表格折行变高，下一轮回窄后留下的残行）。仅终端下生效。
func clearToEndOfScreen() {
	if !termIsTerminal() {
		return
	}
	fmt.Fprint(stdout, "\x1b[0J")
}

// WatchPs 实时刷新状态表：先 resolve 一次固定目标集，之后按 interval 周期重查、原位
// 重绘（光标上移回表顶覆盖上一张表，而非清整屏），直到收到中断信号（Ctrl+C/SIGTERM）。
// 查询与渲染复用 fetchStates/renderPs，与 Ps 的差异只是多一个循环与光标控制。
// interval 是刷新间隔，须为正数（cli 层 --interval/-i 传入，默认 DefaultWatchInterval）。
func (e *Engine) WatchPs(interval time.Duration, names ...string) error {
	if interval <= 0 {
		return fmt.Errorf("刷新间隔必须为正数，实得 %v", interval)
	}
	targets, err := e.resolve(names...)
	if err != nil {
		return err
	}

	done := watchStop
	var stop context.CancelFunc
	if done == nil {
		var ctx context.Context
		ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		done = ctx.Done()
	}
	if stop != nil {
		defer stop()
	}

	prevLines := 0
	for {
		select {
		case <-done:
			return nil
		default:
		}
		all, err := e.fetchStates(targets)
		if err != nil {
			return err
		}
		// 非首轮：光标上移回上一张表顶，新表原位覆盖旧表（不清整屏、不抹掉用户此前的输出）
		moveCursorUp(prevLines)
		lines, err := e.renderPs(targets, all)
		if err != nil {
			return err
		}
		// 首轮之后才清"光标到屏幕末尾"：擦掉上一轮比本轮多出的行（表格变矮时）
		if prevLines > 0 {
			clearToEndOfScreen()
		}
		prevLines = lines
		sleepFunc(interval)
	}
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

// psHeaders 是 ps 表头（6 列）。
var psHeaders = []string{"NAME", "STATUS", "ENABLED", "PID", "MEMORY", "UPTIME"}

// renderPs 按给定状态数据渲染状态表（names 是展示顺序），返回渲染出的行数（供 WatchPs
// 原位重绘时把光标上移这么多行回到表顶）。与 fetchStates 分开，
// 便于 up 先查一次状态做启动复核、再用同一份数据画表，不重复调用 systemctl。
func (e *Engine) renderPs(names []string, all map[string]map[string]string) (int, error) {
	// 开机时长只读一次，供全部服务的 UPTIME 列复用
	boot, bootErr := readUptime()
	on := colorsOn()
	syncTextColors(on)

	t := table.NewWriter()
	t.AppendHeader(toRow(psHeaders))
	for _, name := range names {
		svc := e.cfg.Services[name]
		fields, ok := all[e.unitName(name)]
		if !ok {
			// Show 完全失败（如 systemctl 不可用/无任何输出）时按空状态展示；
			// 未安装的 unit 在正常 systemd 下能取到 LoadState=not-found，会走下方 not-found 分支
			t.AppendRow(toRow([]string{name, "-", "-", "-", "-", "-"}))
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
		t.AppendRow(toRow([]string{
			name,
			colors.Sprint(mergedState(fields["ActiveState"], fields["SubState"], fields["LoadState"])),
			valueOrDash(fields["UnitFileState"]),
			pidOrDash(fields["MainPID"]),
			memoryCell(e.unitName(name), svc, fields, on),
			up,
		}))
	}
	t.SetStyle(table.StyleLight)
	// 先渲染到缓冲区再写 stdout：边框字符要统一着暗灰色（dimBorders），
	// go-pretty 直接 mirror 到 stdout 就拿不到渲染结果了
	buf := &bytes.Buffer{}
	t.SetOutputMirror(buf)
	t.Render()
	raw := buf.String()
	if _, err := fmt.Fprint(stdout, dimBorders(raw, on)); err != nil {
		return 0, err
	}
	// go-pretty 渲染结果每行以 \n 结尾，换行数即表高，供 WatchPs 原位重绘时上移回表顶。
	return strings.Count(raw, "\n"), nil
}

// toRow 把字符串切片转成 go-pretty 的 Row（[]interface{}）。
func toRow(cells []string) table.Row {
	row := make(table.Row, len(cells))
	for i, c := range cells {
		row[i] = c
	}
	return row
}

// cgroupBase 是 cgroup 挂载根目录。变量形式便于测试覆盖（同 UnitDir 的模式）。
// cgroup v2（统一层级）下 systemd 系统级 unit 的目录是 <root>/system.slice/<unit>.service/，
// cgroup v1 或混合层级下该路径不存在，读不到时内存展示自动回退 systemd 的 MemoryCurrent。
var cgroupBase = "/sys/fs/cgroup"

// memoryCell 组装 ps 的 MEMORY 单元格：展示服务的**匿名内存**（堆/栈/私有数据），
// 与系统监视器对单个进程显示的"内存"口径一致——不含可回收的文件页缓存（程序代码、共享库、
// 读过的数据文件），也不含共享内存/内核 slab/页表等开销。这样 app3 这类只加载代码、不做业务
// 文件 I/O 的服务，显示的就是它真正"用掉"的内存，而不是被页缓存撑大的 memory.current。
// 优先读 cgroup v2 memory.stat 的 anon（按 service 聚合全部进程），读不到 cgroup（v1/混合层级/
// 无权限）或 memory.stat 无 anon 行时回退 systemd 的 MemoryCurrent。
// 配置了 memory_max 时追加 " / 上限"，占用比按同一 anon 口径算——anon 逼近上限才真的会 OOM，
// 文件页缓存可被内核回收、逼近上限也不会触发 OOM，用含缓存的 current 算反而会对只做文件 I/O 的
// 服务虚报警告。拿不到内存数据（unit 未安装、MemoryCurrent 为 "[not set]"/2^64 哨兵且读不到 anon）
// 时统一显示 "-"（上限一并不显示），不把 systemd 的内部字面量透给用户。
func memoryCell(unitName string, svc *config.Service, fields map[string]string, on bool) string {
	used, have := serviceMemory(unitName, fields)
	if !have {
		return "-"
	}
	shown := formatUintBytes(used)
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
	if mark, colors := memoryWarn(used * 100 / max); mark != "" {
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

// memoryWarn 按内存占用比（匿名内存 / memory_max，百分比）返回警示标记与配色：
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

// serviceMemory 返回服务的内存占用（匿名内存，与系统监视器口径一致）：
// 优先读 cgroup v2 memory.stat 的 anon（service 内全部进程的堆/栈/私有数据），
// 读不到 cgroup（v1/混合层级/无权限）或 memory.stat 无 anon 行时回退 systemd 的 MemoryCurrent。
// 不回退时用 memCurrentBytes 统一处理 "[not set]"/2^64 哨兵值（返回 ok=false）。
func serviceMemory(unitName string, fields map[string]string) (uint64, bool) {
	if stat, ok := readMemoryStat(filepath.Join(cgroupBase, "system.slice", unitName, "memory.stat")); ok {
		if anon, ok2 := stat["anon"]; ok2 {
			return anon, true
		}
	}
	return memCurrentBytes(fields["MemoryCurrent"])
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

// formatUintBytes 把字节数格式化为友好单位，统一采用二进制（IEC，1024 进制）的 B/KiB/MiB/GiB/TiB，
// 数值保留一位小数。这与 systemd 的体积口径一致（memory_max 的 M/G 即 1024 进制），
// 让 `memory_max: 300M` 显示为 "300.0 MiB" 而非十进制换算出的 "314.6 MB"，写入值能原样读回。
// 单位取 IEC 的 "MiB" 而非系统监视器默认的十进制 "MB"，避免 "MB" 在 1000/1024 进制上的歧义。
func formatUintBytes(n uint64) string {
	switch {
	case n >= 1<<40:
		return fmt.Sprintf("%.1f TiB", float64(n)/(1<<40))
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
