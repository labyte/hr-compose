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
	// 期望 UPTIME=50m、description 都出现在表中，且 CONFIG 列已移除
	out := runPs(t, map[string]*config.Service{
		"api": {Command: "/x/api", Description: "主业务 API"},
	})
	for _, want := range []string{
		"NAME", "STATUS", "ENABLED", "PID", "MEMORY", "UPTIME", "DESCRIPTION",
		"api", "running", "enabled", "1.0M", "50m", "主业务 API",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应包含 %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "CONFIG") {
		t.Errorf("ps 表不应再包含 CONFIG 列\n%s", out)
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

func TestSnipToWidth(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"短于上限原样返回", "zs127", descWidthMax, "zs127"},
		{"恰好等于上限原样返回", strings.Repeat("a", 40), 40, strings.Repeat("a", 40)},
		{"超一个字符即截断补省略号", strings.Repeat("a", 41), 40, strings.Repeat("a", 37) + "..."},
		// 汉字按 2 列计：18 个汉字 36 列 + "..." 3 列 = 39，再塞一个就超出 40 列
		{"中文按显示宽度而非字符数截断", strings.Repeat("生", 25), 40, strings.Repeat("生", 18) + "..."},
		{"空值原样返回", "", 40, ""},
	}
	for _, tc := range cases {
		got := snipToWidth(tc.in, tc.max)
		if got != tc.want {
			t.Errorf("%s: snipToWidth = %q, want %q", tc.name, got, tc.want)
		}
		// 截断结果不得超过限定列宽，否则右边框会被顶歪
		if w := text.StringWidthWithoutEscSequences(got); w > tc.max {
			t.Errorf("%s: 结果显示宽度 %d 超过上限 %d（%q）", tc.name, w, tc.max, got)
		}
	}
}

// 描述过长时必须截断成一行，而不是 go-pretty 默认的折行——折行会让一个服务占多行、整表散架。
func TestPsDescriptionTruncatedNotWrapped(t *testing.T) {
	oldOut, oldOverride := stdout, colorOverride
	stdout = &bytes.Buffer{}
	colorOverride = "never"
	defer func() { stdout, colorOverride = oldOut, oldOverride }()

	longCJK := "反复读取磁盘上的大文件以制造可回收页缓存用于验证内存口径是否正常显示不出错"
	longASCII := "a very long ascii description that surely exceeds the forty column limit"
	// resolve 无参时按名排序：api、reader
	out := runPs(t, map[string]*config.Service{
		"api":    {Command: "/x/api", Description: longASCII},
		"reader": {Command: "/x/read", Description: longCJK},
	})

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// 上边框 + 表头 + 表头分隔线 + 2 行数据 + 下边框 = 6 行；一旦折行，数据行会翻倍
	if len(lines) != 6 {
		t.Errorf("描述超长时每个服务仍应只占一行（期望 6 行，实得 %d 行）:\n%s", len(lines), out)
	}
	// 每行显示宽度必须一致，否则边框错位（中文按 2 列计，故用 go-pretty 的计宽函数）
	border := text.StringWidthWithoutEscSequences(lines[0])
	for i, l := range lines {
		if w := text.StringWidthWithoutEscSequences(l); w != border {
			t.Errorf("第 %d 行显示宽度 %d ≠ 边框 %d，表格错位:\n%s", i+1, w, border, out)
		}
	}
	if !strings.Contains(out, "...") {
		t.Errorf("超长描述应以省略号结尾:\n%s", out)
	}
	if strings.Contains(out, longCJK) || strings.Contains(out, longASCII) {
		t.Errorf("超长描述应被截断，不该原样出现:\n%s", out)
	}
	// 短描述不受影响
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "DESCRIPTION") {
		t.Errorf("表头缺失:\n%s", out)
	}
}

func TestDescWidth(t *testing.T) {
	// 前 6 列显示宽度：NAME 4 / STATUS 7 / ENABLED 7 / PID 7 / MEMORY 18（"38.8M (+100K 缓存)"，
	// 汉字按 2 列计）/ UPTIME 6，加 8 条竖线与每列 2 个空格内边距，固定部分共 69 列
	wideRows := [][]string{{
		"app1", "running", "enabled", "2369314", "38.8M (+100K 缓存)", "35m11s", "第 7 列不参与固定宽度计算",
	}}
	cases := []struct {
		name     string
		termCols int
		rows     [][]string
		want     int
	}{
		{"非终端保持上限", 0, wideRows, descWidthMax},
		{"负列宽按非终端处理", -1, wideRows, descWidthMax},
		{"宽终端触上限", 200, wideRows, descWidthMax},
		{"92 列终端把剩余宽度给描述列", 92, wideRows, 21},
		{"恰好容纳满宽描述列", 111, wideRows, descWidthMax},
		{"极窄终端保下限", 40, wideRows, descWidthMin},
		// 无行数据时按表头计固定宽度：4+6+7+3+6+6 加竖线与内边距共 52 列，80-52-2=26
		{"无行数据按表头计宽", 80, nil, 26},
	}
	for _, tc := range cases {
		if got := descWidth(tc.termCols, tc.rows); got != tc.want {
			t.Errorf("%s: descWidth(%d) = %d, want %d", tc.name, tc.termCols, got, tc.want)
		}
	}
}

// 窄终端下描述列必须自动收窄，使整表宽度恰好等于终端列宽：超出终端宽度的表格会被终端
// 拦腰折行（续行没有左边框），比折描述列难看得多。
func TestPsTableFitsTerminalWidth(t *testing.T) {
	oldTerm := termColumnsFunc
	termColumnsFunc = func() int { return 80 }
	defer func() { termColumnsFunc = oldTerm }()

	longCJK := "反复读取磁盘上的大文件以制造可回收页缓存用于验证内存口径是否正常显示不出错"
	out := runPs(t, map[string]*config.Service{
		"api":    {Command: "/x/api", Description: longCJK},
		"reader": {Command: "/x/read", Description: longCJK},
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// 上边框 + 表头 + 表头分隔线 + 2 行数据 + 下边框 = 6 行；一旦折行，数据行会翻倍
	if len(lines) != 6 {
		t.Errorf("每个服务仍应只占一行（期望 6 行，实得 %d 行）:\n%s", len(lines), out)
	}
	// 整表每行显示宽度必须恰好等于终端列宽（go-pretty 会补齐内边距），既不被终端折行也不浪费宽度
	for i, l := range lines {
		if w := text.StringWidthWithoutEscSequences(l); w != 80 {
			t.Errorf("第 %d 行显示宽度 %d ≠ 终端列宽 80:\n%s", i+1, w, out)
		}
	}
	if !strings.Contains(out, "...") {
		t.Errorf("描述列应被截断以适配终端宽度:\n%s", out)
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
