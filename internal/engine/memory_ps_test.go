package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hr.compose/internal/config"
)

// fakeCgroup 把 cgroupBase 指向临时目录，并按给定内容写出 <root>/system.slice/<unit>/memory.stat。
// 返回的清理函数恢复包级变量。
func fakeCgroup(t *testing.T, unitName, stat string) func() {
	t.Helper()
	old := cgroupBase
	dir := t.TempDir()
	cgroupBase = dir
	path := filepath.Join(dir, "system.slice", unitName, "memory.stat")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if stat != "" {
		if err := os.WriteFile(path, []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return func() { cgroupBase = old }
}

const mb = uint64(1 << 20)

func TestWorkingSetBytes(t *testing.T) {
	cases := []struct {
		name    string
		current uint64
		stat    map[string]uint64
		want    uint64
		wantOK  bool
	}{
		// 真机实测的 writer 服务：写 300M 磁盘文件后 memory.current 309.5M，其中 300M 是可回收页缓存
		{"剔除可回收页缓存", 309500000, map[string]uint64{"inactive_file": 300000000}, 9500000, true},
		// child 服务：64M 内存在子进程里，缓存为零 → working set 就是总量
		{"无缓存时等于总量", 70500000, map[string]uint64{"anon": 68200000, "inactive_file": 0}, 70500000, true},
		// inactive_file 理论上不会大于 current，异常数据时夹紧而不是减成回绕的巨值
		{"缓存异常大于总量时夹紧", 10 * mb, map[string]uint64{"inactive_file": 20 * mb}, 10 * mb, true},
		{"stat 缺失不回退到 working set", 10 * mb, nil, 0, false},
	}
	for _, tc := range cases {
		got, ok := workingSetBytes(tc.current, tc.stat)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("%s: workingSetBytes = %d/%v, want %d/%v", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestReadMemoryStat(t *testing.T) {
	defer fakeCgroup(t, "api.service", "anon 12582912\nfile 314572800\ninactive_file 314572800\nslab_packed 0\n")()
	stat, ok := readMemoryStat(filepath.Join(cgroupBase, "system.slice", "api.service", "memory.stat"))
	if !ok {
		t.Fatal("memory.stat 可读时应返回 ok")
	}
	if stat["inactive_file"] != 314572800 || stat["anon"] != 12582912 {
		t.Errorf("解析结果不符: %v", stat)
	}
	if _, ok := readMemoryStat(filepath.Join(cgroupBase, "system.slice", "missing.service", "memory.stat")); ok {
		t.Error("文件不存在时应 ok=false（回退 systemd 口径）")
	}
}

func TestMemoryCellUsesWorkingSet(t *testing.T) {
	// 现口径（MemoryCurrent）把 300M 可回收页缓存算进"内存占用"，working set 剔掉它
	defer fakeCgroup(t, "api.service", "anon 12582912\ninactive_file 300000000\n")()
	fields := map[string]string{"MemoryCurrent": "309500000"}
	got := memoryCell("api.service", &config.Service{}, fields, false)
	if got != "9.1M" {
		t.Errorf("MEMORY = %q, want working set 9.1M（309500000-300000000）", got)
	}
	if strings.Contains(got, "295") {
		t.Errorf("不应再直接展示含页缓存的 MemoryCurrent: %q", got)
	}
}

func TestMemoryCellFallsBackWithoutCgroup(t *testing.T) {
	// cgroup v1 / 混合层级 / 无权限读不到 memory.stat 时，回退 systemd 的 MemoryCurrent
	defer fakeCgroup(t, "api.service", "")()
	got := memoryCell("api.service", &config.Service{}, map[string]string{"MemoryCurrent": "309500000"}, false)
	if got != "295.2M" {
		t.Errorf("读不到 memory.stat 时应回退现口径，got %q want 295.2M", got)
	}
	// 未启用内存统计的哨兵值仍显示 "-"
	got = memoryCell("api.service", &config.Service{}, map[string]string{"MemoryCurrent": "18446744073709551615"}, false)
	if got != "-" {
		t.Errorf("哨兵值应显示 -，got %q", got)
	}
}

func TestMemoryCellShowsLimitAndWarns(t *testing.T) {
	fields := map[string]string{"MemoryCurrent": "300000000"} // ≈286M
	svc := &config.Service{MemoryMax: "300M"}

	// 用量 95%（含缓存的 current 与上限之比）→ 高亮警示
	defer fakeCgroup(t, "api.service", "inactive_file 0\n")()
	got := memoryCell("api.service", svc, fields, false)
	if !strings.HasPrefix(got, "286.1M / 300.0M") {
		t.Errorf("应显示 working set / 上限，got %q", got)
	}
	if !strings.Contains(got, "!!") {
		t.Errorf("逼近上限应有 !! 标记，got %q", got)
	}

	// 80%~90% 用单感叹号（260000000 / 314572800 ≈ 82.6%）
	mid := map[string]string{"MemoryCurrent": "260000000"}
	got = memoryCell("api.service", svc, mid, false)
	if !strings.Contains(got, " ! ") || strings.Contains(got, "!!") {
		t.Errorf("80%%~90%% 应是单感叹号，got %q", got)
	}
	// 恰好 80% 也警示（含缓存的 current 就是判定口径）
	got = memoryCell("api.service", svc, map[string]string{"MemoryCurrent": "251658240"}, false) // 240M/300M
	if !strings.Contains(got, " ! ") {
		t.Errorf("80%% 应警示，got %q", got)
	}

	// 低于 80% 不加噪声
	low := map[string]string{"MemoryCurrent": "100000000"}
	got = memoryCell("api.service", svc, low, false)
	if strings.Contains(got, "!") {
		t.Errorf("用量正常时不该有警示标记，got %q", got)
	}

	// 认不出的上限取值原样展示（如 infinity）
	inf := &config.Service{MemoryMax: "infinity"}
	got = memoryCell("api.service", inf, fields, false)
	if !strings.HasSuffix(got, " / infinity") {
		t.Errorf("无法解析的上限应原样展示，got %q", got)
	}
}

func TestMemoryWarn(t *testing.T) {
	// 断言档位与所选配色（用 EscapeSeq 比对颜色码；ANSI 是否真正输出由终端决定，不该由测试假设）
	cases := []struct {
		pct      uint64
		wantMark string
		wantSeq  string // 期望的 ANSI 颜色码片段
	}{
		{100, "!!", "31"},
		{95, "!!", "31"},
		{90, "!!", "31"},
		{89, "!", "33"},
		{85, "!", "33"},
		{80, "!", "33"},
		{79, "", ""},
		{50, "", ""},
		{0, "", ""},
	}
	for _, tc := range cases {
		mark, colors := memoryWarn(tc.pct)
		if tc.wantMark == "" {
			if mark != "" || len(colors) != 0 {
				t.Errorf("memoryWarn(%d) = %q/%v，用量正常时不该警示", tc.pct, mark, colors)
			}
			continue
		}
		if !strings.Contains(mark, tc.wantMark) {
			t.Errorf("memoryWarn(%d) 标记 = %q，期望含 %q", tc.pct, mark, tc.wantMark)
		}
		if seq := colors.EscapeSeq(); !strings.Contains(seq, tc.wantSeq) {
			t.Errorf("memoryWarn(%d) 配色 = %q，期望含 %q", tc.pct, seq, tc.wantSeq)
		}
	}
}

func TestMemoryCellColorGate(t *testing.T) {
	defer fakeCgroup(t, "api.service", "inactive_file 0\n")()
	svc := &config.Service{MemoryMax: "300M"}
	hot := map[string]string{"MemoryCurrent": "300000000"}

	withColor := memoryCell("api.service", svc, hot, true)
	if !strings.Contains(withColor, "!!") {
		t.Errorf("on=true 时应有警示标记，got %q", withColor)
	}
	// 颜色关闭（管道/重定向）时不得出现 ANSI 码，但标记仍在
	plain := memoryCell("api.service", svc, hot, false)
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("on=false 时不应有 ANSI 码: %q", plain)
	}
	if !strings.Contains(plain, "!!") {
		t.Errorf("on=false 时标记仍应保留: %q", plain)
	}
}

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"500M", 500 * mb, true},
		{"500MiB", 500 * mb, true},
		{"500 MB", 500 * mb, true},
		{"1.5G", 1536 * mb, true},
		{"2048K", 2048 * 1024, true},
		{"1073741824", 1073741824, true},
		{"2T", 2 * 1024 * 1024 * mb, true},
		{"80%", 0, false}, // CPUQuota 写法，不是体积
		{"infinity", 0, false},
		{"", 0, false},
		{"-1M", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseByteSize(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseByteSize(%q) = %d/%v, want %d/%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// ps 渲染出的 MEMORY 列应带上限（配置了 memory_max 时），且不因颜色开关错位。
func TestPsMemoryColumnWithLimit(t *testing.T) {
	oldOut, oldOverride := stdout, colorOverride
	stdout = &bytes.Buffer{}
	colorOverride = "never"
	defer func() { stdout, colorOverride = oldOut, oldOverride }()
	defer fakeCgroup(t, "api.service", "inactive_file 0\n")()

	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", MemoryMax: "300M"},
	}}
	// fakeSys 的 MemoryCurrent=1048576（1.0M），上限 300M → 用量 <80%，无警示标记
	if err := New(cfg, &fakeSys{}).Ps(); err != nil {
		t.Fatal(err)
	}
	out := stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "1.0M / 300.0M") {
		t.Errorf("ps 的 MEMORY 列应含上限，输出:\n%s", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("colorOverride=never 时不应有 ANSI 码:\n%s", out)
	}
}

func TestMemoryCellZeroLimitNoPanic(t *testing.T) {
	// memory_max 写成 "0" / "0K" / "0.0M" 一类时上限解析为 0：ps 是只读命令，
	// 用 current*100/max 算占用比会整数除零 panic（曾实测复现）。
	// 上限本身无效（systemd 语义等于不允许占用内存），展示 " / 0B" 即可，警示交给 up 的启动复核。
	defer fakeCgroup(t, "api.service", "inactive_file 0\n")()
	fields := map[string]string{"MemoryCurrent": "300000000"} // ≈286.1M
	for _, limit := range []string{"0", "0K", "0.0M"} {
		got := memoryCell("api.service", &config.Service{MemoryMax: limit}, fields, false)
		if got != "286.1M / 0B" {
			t.Errorf("上限 %q 应展示用量与 0 上限，got %q", limit, got)
		}
		if strings.Contains(got, "!") {
			t.Errorf("上限 %q 时不应计算占用比、不应有警示标记，got %q", limit, got)
		}
	}
}

func TestPsWithZeroMemoryMaxNoPanic(t *testing.T) {
	// 端到端兜底：整表渲染路径（Ps → renderPs → memoryCell）同样不得 panic。
	out := capturePsOutput(t)
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", MemoryMax: "0"},
	}}
	if err := New(cfg, &fakeSys{}).Ps(); err != nil {
		t.Fatal(err)
	}
	// fakeSys 的 MemoryCurrent=1048576（1.0M）
	if s := out.String(); !strings.Contains(s, "1.0M / 0B") {
		t.Errorf("ps 应展示用量与 0 上限，输出:\n%s", s)
	}
}
