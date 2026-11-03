package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jedib0t/go-pretty/v6/text"

	"hr.compose/internal/config"
)

func TestStateColors(t *testing.T) {
	cases := []struct {
		active string
		want   bool // 是否非 nil
	}{
		{"active", true},
		{"failed", true},
		{"activating", true},
		{"deactivating", true},
		{"reloading", true},
		{"inactive", true},
		{"", false},
		{"unknown", false},
		{"-", false},
	}
	for _, tc := range cases {
		if got := stateColors(tc.active); (got != nil) != tc.want {
			t.Errorf("stateColors(%q) non-nil=%v, want %v", tc.active, got != nil, tc.want)
		}
	}
}

func TestMergedState(t *testing.T) {
	cases := []struct{ active, sub, load, want string }{
		{"active", "running", "loaded", "running"},             // 运行中
		{"active", "", "loaded", "active"},                     // 空子状态，回退主状态
		{"active", "exited", "loaded", "exited"},               // oneshot 已完成
		{"active", "waiting", "loaded", "waiting"},             // notify 未就绪
		{"inactive", "dead", "loaded", "stopped"},              // 已安装但停止
		{"inactive", "dead", "", "stopped"},                    // LoadState 缺失时按已安装处理（兼容旧输出）
		{"inactive", "dead", "not-found", "not-found"},         // 未安装（unit 不存在，含 down 后），优先于 stopped
		{"active", "", "not-found", "not-found"},               // not-found 优先于其他组合
		{"failed", "failed", "loaded", "failed"},               // 子状态与主状态相同，省略
		{"failed", "", "loaded", "failed"},                     // 空子状态
		{"activating", "start", "loaded", "starting"},          // 启动中
		{"activating", "start-pre", "loaded", "starting"},      // 启动中（前置步骤）
		{"activating", "auto-restart", "loaded", "restarting"}, // 自动重启中，单独表达
		{"deactivating", "stop", "loaded", "stopping"},         // 停止中
		{"deactivating", "stop-sigterm", "loaded", "stopping"}, // 内部细节不展示
		{"reloading", "reload", "loaded", "reloading"},         // 重载中
		{"active", "degraded", "loaded", "active/degraded"},    // 罕见组合回退两级
		{"inactive", "exited", "loaded", "inactive/exited"},    // 罕见组合回退两级
	}
	for _, tc := range cases {
		if got := mergedState(tc.active, tc.sub, tc.load); got != tc.want {
			t.Errorf("mergedState(%q, %q, %q) = %q, want %q", tc.active, tc.sub, tc.load, got, tc.want)
		}
	}
}

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{time.Minute, "1m"},
		{5*time.Minute + 30*time.Second, "5m30s"},
		{time.Hour, "1h"},
		{2*time.Hour + 5*time.Minute, "2h5m"},
		{3 * 24 * time.Hour, "3d"},
		{3*24*time.Hour + 4*time.Hour, "3d4h"},
		{0, "-"},
		{-time.Second, "-"},
	}
	for _, tc := range cases {
		if got := formatUptime(tc.d); got != tc.want {
			t.Errorf("formatUptime(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestUptimeSince(t *testing.T) {
	cases := []struct {
		name, monotonic, boot string
		wantOK                bool
		want                  time.Duration
	}{
		{"正常", "600000000", "3600.00", true, 50 * time.Minute},
		{"未启动", "0", "3600.00", false, 0},
		{"空字段", "", "3600.00", false, 0},
		{"非法单调值", "abc", "3600.00", false, 0},
		{"启动时刻晚于开机（时钟异常）", "3600000000", "3600.00", false, 0},
	}
	for _, tc := range cases {
		got, ok := uptimeSince(tc.monotonic, tc.boot)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("uptimeSince(%q, %q) = (%v, %v), want (%v, %v)",
				tc.monotonic, tc.boot, got, ok, tc.want, tc.wantOK)
		}
	}
}

// writeUptime 写一个临时 uptime 文件并返回路径，用于覆盖 uptimeFile 变量。
func writeUptime(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "uptime")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// runPs 用 fakeSys 执行 Ps 并把输出捕获到缓冲，返回文本。
// 固定开机时长 1h，配合 fakeShowFields 的 ExecMainStartTimestampMonotonic=600s（启动 10 分钟）得出 UPTIME=50m。
func runPs(t *testing.T, services map[string]*config.Service) string {
	t.Helper()
	old := uptimeFile
	uptimeFile = writeUptime(t, "3600.00 1800.00")
	defer func() { uptimeFile = old }()

	cfg := &config.Config{Services: services}
	buf := &bytes.Buffer{}
	stdout = buf
	if err := New(cfg, &fakeSys{}).Ps(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestPsColored(t *testing.T) {
	oldOut, oldOverride := stdout, colorOverride
	stdout = &bytes.Buffer{}
	colorOverride = "always"
	defer func() { stdout, colorOverride = oldOut, oldOverride }()

	out := runPs(t, map[string]*config.Service{"api": {Command: "/x/api"}})
	want := text.Colors{text.FgGreen}.Sprint("running")
	if !strings.Contains(out, want) {
		t.Errorf("running 状态应染绿（含 %q）\n%s", want, out)
	}
}

func TestPsPlainWhenColorOff(t *testing.T) {
	oldOut, oldOverride := stdout, colorOverride
	stdout = &bytes.Buffer{}
	colorOverride = "never"
	defer func() { stdout, colorOverride = oldOut, oldOverride }()

	out := runPs(t, map[string]*config.Service{"api": {Command: "/x/api"}})
	if strings.Contains(out, "\x1b[") {
		t.Errorf("颜色关闭时不应输出 ANSI 转义码\n%s", out)
	}
}

func TestPsColumns(t *testing.T) {
	oldOut, oldOverride := stdout, colorOverride
	stdout = &bytes.Buffer{}
	colorOverride = "never"
	defer func() { stdout, colorOverride = oldOut, oldOverride }()

	// fakeSys.Show 返回 active/running/enabled/123/1048576 + 单调启动时刻；
	// 期望 UPTIME=50m，且 DESCRIPTION 列已移除
	out := runPs(t, map[string]*config.Service{
		"api": {Command: "/x/api", Description: "主业务 API"},
	})
	for _, want := range []string{
		"NAME", "STATUS", "ENABLED", "PID", "MEMORY", "UPTIME",
		"api", "running", "enabled", "1.0 MiB", "50m",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应包含 %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "DESCRIPTION") {
		t.Errorf("ps 表不应再包含 DESCRIPTION 列\n%s", out)
	}
	if strings.Contains(out, "主业务 API") {
		t.Errorf("ps 表不应再展示描述内容\n%s", out)
	}
}

// partialFake 模拟批量查询缺失某 unit，但逐服务 Show 仍能查到（回退路径）。
type partialFake struct {
	*fakeSys
}

func (f *partialFake) ShowMany(units []string) (map[string]map[string]string, error) {
	m := map[string]map[string]string{}
	for _, u := range units {
		if u != "redis.service" {
			m[u] = fakeShowFields()
		}
	}
	return m, nil
}

func TestPsFallbackPerUnitWhenMissingFromBatch(t *testing.T) {
	oldOut, oldOverride := stdout, colorOverride
	stdout = &bytes.Buffer{}
	colorOverride = "never"
	defer func() { stdout, colorOverride = oldOut, oldOverride }()

	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api"},
		"redis": {Command: "/x/redis"},
	}}
	if err := New(cfg, &partialFake{&fakeSys{}}).Ps(); err != nil {
		t.Fatal(err)
	}
	out := stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "running") {
		t.Errorf("redis 缺失于批量结果时应通过 Show 回退取到 running\n%s", out)
	}
}

// notFoundFake 模拟某服务未安装（LoadState=not-found，unit 文件不存在，含 down 后），其余服务正常。
type notFoundFake struct {
	*fakeSys
}

func (f *notFoundFake) ShowMany(units []string) (map[string]map[string]string, error) {
	m := map[string]map[string]string{}
	for _, u := range units {
		if u == "redis.service" {
			fields := fakeShowFields()
			fields["ActiveState"] = "inactive"
			fields["SubState"] = "dead"
			fields["LoadState"] = "not-found"
			fields["UnitFileState"] = ""
			fields["MainPID"] = "0"
			m[u] = fields
		} else {
			m[u] = fakeShowFields()
		}
	}
	return m, nil
}

func TestPsNotInstalledShowsNotFound(t *testing.T) {
	oldOut, oldOverride := stdout, colorOverride
	stdout = &bytes.Buffer{}
	colorOverride = "never"
	defer func() { stdout, colorOverride = oldOut, oldOverride }()

	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api"},
		"redis": {Command: "/x/redis"},
	}}
	if err := New(cfg, &notFoundFake{&fakeSys{}}).Ps(); err != nil {
		t.Fatal(err)
	}
	out := stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "not-found") {
		t.Errorf("未安装的服务应展示 not-found 而非 stopped\n%s", out)
	}
	if strings.Contains(out, "stopped") {
		t.Errorf("未安装的服务不应展示 stopped\n%s", out)
	}
}

// countingSys 记录 ShowMany 调用次数，并在达到 stopAt 次后关闭 stopCh，用于驱动 ps --watch
// 的循环在有限轮后确定性地退出（真实中断信号在单测里不易注入）。
type countingSys struct {
	fakeSys
	showMany int
	stopAt   int
	stopCh   chan struct{}
}

func (c *countingSys) ShowMany(units []string) (map[string]map[string]string, error) {
	c.showMany++
	if c.stopCh != nil && c.showMany >= c.stopAt {
		select {
		case <-c.stopCh:
		default:
			close(c.stopCh)
		}
	}
	return c.fakeSys.ShowMany(units)
}

func TestWatchPsLoopsUntilStop(t *testing.T) {
	oldStop := watchStop
	stopCh := make(chan struct{})
	watchStop = stopCh
	defer func() { watchStop = oldStop }()

	sys := &countingSys{stopAt: 3, stopCh: stopCh}
	out := capturePsOutput(t)
	cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/x/api"}}}
	if err := New(cfg, sys).WatchPs(); err != nil {
		t.Fatal(err)
	}
	if sys.showMany != 3 {
		t.Errorf("应在 3 轮后停止刷新，实得 %d 轮", sys.showMany)
	}
	// 每轮渲染一份完整表格（各含一个 NAME 表头），三轮应出现 3 个 NAME
	if got := strings.Count(out.String(), "NAME"); got != 3 {
		t.Errorf("应渲染 3 份状态表（3 个 NAME 表头），实得 %d 个\n%s", got, out.String())
	}
}

func TestMoveCursorUp(t *testing.T) {
	oldOut, oldTerm := stdout, termIsTerminal
	stdout = &bytes.Buffer{}
	termIsTerminal = func() bool { return true }
	defer func() { stdout, termIsTerminal = oldOut, oldTerm }()

	moveCursorUp(3)
	if got := stdout.(*bytes.Buffer).String(); got != "\x1b[3A" {
		t.Errorf("moveCursorUp(3) 应输出上移序列，got %q", got)
	}

	// 0 或负数不输出
	stdout = &bytes.Buffer{}
	moveCursorUp(0)
	if got := stdout.(*bytes.Buffer).String(); got != "" {
		t.Errorf("moveCursorUp(0) 不应输出，got %q", got)
	}

	// 非终端（管道/重定向）不输出，避免往文件里塞 ANSI 转义码
	termIsTerminal = func() bool { return false }
	stdout = &bytes.Buffer{}
	moveCursorUp(3)
	if got := stdout.(*bytes.Buffer).String(); got != "" {
		t.Errorf("非终端下 moveCursorUp 不应输出，got %q", got)
	}
}

func TestClearToEndOfScreen(t *testing.T) {
	oldOut, oldTerm := stdout, termIsTerminal
	stdout = &bytes.Buffer{}
	termIsTerminal = func() bool { return true }
	defer func() { stdout, termIsTerminal = oldOut, oldTerm }()

	clearToEndOfScreen()
	if got := stdout.(*bytes.Buffer).String(); got != "\x1b[0J" {
		t.Errorf("终端下 clearToEndOfScreen 应输出清尾序列，got %q", got)
	}

	termIsTerminal = func() bool { return false }
	stdout = &bytes.Buffer{}
	clearToEndOfScreen()
	if got := stdout.(*bytes.Buffer).String(); got != "" {
		t.Errorf("非终端下 clearToEndOfScreen 不应输出，got %q", got)
	}
}

// 原位重绘：首轮直接打印表格（不做光标上移），之后每轮先上移回上一张表顶覆盖重绘，
// 并在重绘后清"光标到屏幕末尾"（擦掉变矮时残留的行）。
func TestWatchPsRedrawsInPlace(t *testing.T) {
	oldStop, oldTerm := watchStop, termIsTerminal
	stopCh := make(chan struct{})
	watchStop = stopCh
	termIsTerminal = func() bool { return true }
	defer func() { watchStop, termIsTerminal = oldStop, oldTerm }()

	sys := &countingSys{stopAt: 3, stopCh: stopCh}
	out := capturePsOutput(t)
	cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/x/api"}}}
	if err := New(cfg, sys).WatchPs(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	// 单服务表高 5 行：上边框 + 表头 + 分隔线 + 1 数据行 + 下边框；第 2、3 轮各上移 5 行覆盖旧表
	if got := strings.Count(s, "\x1b[5A"); got != 2 {
		t.Errorf("第 2、3 轮应各上移 5 行覆盖旧表，实得 %d 次\n%s", got, s)
	}
	// 非首轮每轮重绘后清尾一次（首轮不清，避免抹掉用户此前的输出）
	if got := strings.Count(s, "\x1b[0J"); got != 2 {
		t.Errorf("非首轮每轮都应清尾，实得 %d 次\n%s", got, s)
	}
}

// 边框着色为暗灰（视觉上更细更暗）只应作用于边框字符：状态列配色保持、数据单元格不染色，
// 颜色关闭（管道/重定向/NO_COLOR）时不塞任何转义码。
func TestPsBordersDimmed(t *testing.T) {
	oldOverride := colorOverride
	colorOverride = "always"
	defer func() { colorOverride = oldOverride }()

	out := runPs(t, map[string]*config.Service{"api": {Command: "/x/api"}})
	if dim := borderColors.Sprint("│"); !strings.Contains(out, dim) {
		t.Errorf("颜色开启时边框字符应为暗灰 %q:\n%s", dim, out)
	}
	// 状态列自己的配色不受边框着色影响
	if green := (text.Colors{text.FgGreen}).Sprint("running"); !strings.Contains(out, green) {
		t.Errorf("状态列 running 的绿色应保留:\n%s", out)
	}
	// 数据单元格里的普通字符不得被误染成边框色
	if strings.Contains(out, borderColors.Sprint("a")) {
		t.Errorf("数据单元格不应被染成边框色:\n%s", out)
	}

	colorOverride = "never"
	out = runPs(t, map[string]*config.Service{"api": {Command: "/x/api"}})
	if strings.Contains(out, "\x1b[") {
		t.Errorf("颜色关闭时输出不应含 ANSI 转义码:\n%q", out)
	}
}
