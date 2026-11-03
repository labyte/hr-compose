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

func TestServiceMemory(t *testing.T) {
	cases := []struct {
		name   string
		stat   string // fakeCgroup 写入的 memory.stat 内容（"" 表示不写文件 → 读不到）
		fields map[string]string
		want   uint64
		wantOK bool
	}{
		// 有 anon 行 → 用 anon（匿名内存）。MemoryCurrent 被页缓存撑大也不影响。
		{"anon 优先", "anon 12582912\nfile 314572800\n", map[string]string{"MemoryCurrent": "309500000"}, 12582912, true},
		// stat 无 anon 行 → 回退 MemoryCurrent
		{"无 anon 行回退 current", "file 314572800\n", map[string]string{"MemoryCurrent": "309500000"}, 309500000, true},
		// stat 读不到（文件不存在/无权限）→ 回退 MemoryCurrent
		{"读不到 stat 回退 current", "", map[string]string{"MemoryCurrent": "309500000"}, 309500000, true},
		// 哨兵值 → 不可用
		{"哨兵值", "", map[string]string{"MemoryCurrent": "18446744073709551615"}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer fakeCgroup(t, "api.service", tc.stat)()
			got, ok := serviceMemory("api.service", tc.fields)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("serviceMemory = %d/%v, want %d/%v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestReadMemoryStat(t *testing.T) {
	defer fakeCgroup(t, "api.service", "anon 12582912\nfile 314572800\nslab_packed 0\n")()
	stat, ok := readMemoryStat(filepath.Join(cgroupBase, "system.slice", "api.service", "memory.stat"))
	if !ok {
		t.Fatal("memory.stat 可读时应返回 ok")
	}
	if stat["anon"] != 12582912 || stat["file"] != 314572800 {
		t.Errorf("解析结果不符: %v", stat)
	}
	if _, ok := readMemoryStat(filepath.Join(cgroupBase, "system.slice", "missing.service", "memory.stat")); ok {
		t.Error("文件不存在时应 ok=false（回退 systemd 口径）")
	}
}

func TestMemoryCellUsesAnon(t *testing.T) {
	// 口径：展示匿名内存 anon（12.0 MiB），而不是被页缓存撑大的 MemoryCurrent（295.2 MiB）。
	// 系统监视器对 app3 这类"只加载代码、不做业务文件 I/O"的服务显示的就是 anon。
	defer fakeCgroup(t, "api.service", "anon 12582912\nfile 314572800\n")()
	fields := map[string]string{"MemoryCurrent": "309500000"}
	got := memoryCell("api.service", &config.Service{}, fields, false)
	if got != "12.0 MiB" {
		t.Errorf("MEMORY = %q, want \"12.0 MiB\"（anon 12582912）", got)
	}
	if strings.Contains(got, "295") || strings.Contains(got, "缓存") {
		t.Errorf("不应展示含页缓存的 MemoryCurrent 或缓存段: %q", got)
	}
}

func TestMemoryCellFallsBackWithoutCgroup(t *testing.T) {
	// cgroup v1 / 混合层级 / 无权限读不到 memory.stat 时，回退 systemd 的 MemoryCurrent
	defer fakeCgroup(t, "api.service", "")()
	got := memoryCell("api.service", &config.Service{}, map[string]string{"MemoryCurrent": "309500000"}, false)
	if got != "295.2 MiB" {
		t.Errorf("读不到 memory.stat 时应回退 MemoryCurrent，got %q want 295.2 MiB", got)
	}
	// 未启用内存统计的哨兵值仍显示 "-"
	got = memoryCell("api.service", &config.Service{}, map[string]string{"MemoryCurrent": "18446744073709551615"}, false)
	if got != "-" {
		t.Errorf("哨兵值应显示 -，got %q", got)
	}
}

func TestMemoryCellShowsLimitAndWarns(t *testing.T) {
	// 占用比按 anon 算（与展示口径一致），不再按含页缓存的 current 算。
	fields := map[string]string{"MemoryCurrent": "309500000"}
	svc := &config.Service{MemoryMax: "300M"}

	// 匿名内存 300M（314572800 字节）/ 300M = 100% → 红色 !!
	t.Run("100%", func(t *testing.T) {
		defer fakeCgroup(t, "api.service", "anon 314572800\n")()
		got := memoryCell("api.service", svc, fields, false)
		if !strings.HasPrefix(got, "300.0 MiB / 300.0 MiB") {
			t.Errorf("应显示 anon / 上限，got %q", got)
		}
		if !strings.Contains(got, "!!") {
			t.Errorf("逼近上限应有 !! 标记，got %q", got)
		}
	})

	// 80%~90% 用单感叹号（260000000 / 314572800 ≈ 82.6%）
	t.Run("82.6%", func(t *testing.T) {
		defer fakeCgroup(t, "api.service", "anon 260000000\n")()
		got := memoryCell("api.service", svc, fields, false)
		if !strings.Contains(got, " ! ") || strings.Contains(got, "!!") {
			t.Errorf("80%%~90%% 应是单感叹号，got %q", got)
		}
	})

	// 认不出的上限取值原样展示（如 infinity）
	t.Run("infinity", func(t *testing.T) {
		defer fakeCgroup(t, "api.service", "anon 314572800\n")()
		got := memoryCell("api.service", &config.Service{MemoryMax: "infinity"}, fields, false)
		if !strings.HasSuffix(got, " / infinity") {
			t.Errorf("无法解析的上限应原样展示，got %q", got)
		}
	})
}

func TestMemoryCellFileCacheDoesNotTriggerWarn(t *testing.T) {
	// 回归 app3 场景：anon 只有 9M，但 page cache（file）有 300M。
	// 旧口径按含缓存的 current 算占用比会误报"接近上限"；现在按 anon 算，9.0 MiB/320.0 MiB 远低于 80%，不该警示。
	defer fakeCgroup(t, "api.service", "anon 9437184\nfile 314572800\n")()
	fields := map[string]string{"MemoryCurrent": "323800064"}
	svc := &config.Service{MemoryMax: "320M"}

	got := memoryCell("api.service", svc, fields, false)
	if !strings.HasPrefix(got, "9.0 MiB / 320.0 MiB") {
		t.Errorf("MEMORY = %q, want 前缀 \"9.0 MiB / 320.0 MiB\"（anon 9437184）", got)
	}
	if strings.Contains(got, "!") {
		t.Errorf("页缓存不该触发上限警示，got %q", got)
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
	defer fakeCgroup(t, "api.service", "anon 314572800\n")()
	svc := &config.Service{MemoryMax: "300M"}
	hot := map[string]string{"MemoryCurrent": "314572800"}

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
	defer fakeCgroup(t, "api.service", "anon 1048576\n")()

	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", MemoryMax: "300M"},
	}}
	// anon=1048576（1.0 MiB），上限 300M → 用量 <80%，无警示标记
	if err := New(cfg, &fakeSys{}).Ps(); err != nil {
		t.Fatal(err)
	}
	out := stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "1.0 MiB / 300.0 MiB") {
		t.Errorf("ps 的 MEMORY 列应含上限，输出:\n%s", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("colorOverride=never 时不应有 ANSI 码:\n%s", out)
	}
}

func TestMemoryCellZeroLimitNoPanic(t *testing.T) {
	// memory_max 写成 "0" / "0K" / "0.0M" 一类时上限解析为 0：ps 是只读命令，
	// 用 anon*100/max 算占用比会整数除零 panic（曾实测复现）。
	// 上限本身无效（systemd 语义等于不允许占用内存），展示 " / 0 B" 即可，警示交给 up 的启动复核。
	defer fakeCgroup(t, "api.service", "anon 300000000\n")()
	fields := map[string]string{"MemoryCurrent": "300000000"} // ≈286.1 MiB
	for _, limit := range []string{"0", "0K", "0.0M"} {
		got := memoryCell("api.service", &config.Service{MemoryMax: limit}, fields, false)
		if got != "286.1 MiB / 0 B" {
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
	defer fakeCgroup(t, "api.service", "anon 1048576\n")()
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", MemoryMax: "0"},
	}}
	if err := New(cfg, &fakeSys{}).Ps(); err != nil {
		t.Fatal(err)
	}
	// anon=1048576（1.0 MiB）
	if s := out.String(); !strings.Contains(s, "1.0 MiB / 0 B") {
		t.Errorf("ps 应展示用量与 0 上限，输出:\n%s", s)
	}
}
