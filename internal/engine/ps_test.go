package engine

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"hr.compose/internal/config"
)

func TestStateColor(t *testing.T) {
	cases := []struct {
		active string
		want   string
	}{
		{"active", colorGreen},
		{"failed", colorRed},
		{"activating", colorYellow},
		{"deactivating", colorYellow},
		{"reloading", colorYellow},
		{"inactive", colorGray},
		{"", ""},
		{"unknown", ""},
		{"-", ""},
	}
	for _, tc := range cases {
		if got := stateColor(tc.active); got != tc.want {
			t.Errorf("stateColor(%q) = %q, want %q", tc.active, got, tc.want)
		}
	}
}

// runPs 用 fakeSys 执行 Ps 并把输出捕获到缓冲，返回文本。
func runPs(t *testing.T, services map[string]*config.Service) string {
	t.Helper()
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
	// fakeSys 返回 ActiveState=active，应被染绿
	if !strings.Contains(out, colorGreen) {
		t.Errorf("active 状态应包含绿色转义码\n%s", out)
	}
	if !strings.Contains(out, "api") {
		t.Errorf("输出应包含服务名 api\n%s", out)
	}
}

func TestPsPlainWhenColorOff(t *testing.T) {
	oldOut, oldOverride := stdout, colorOverride
	stdout = &bytes.Buffer{}
	colorOverride = "never"
	defer func() { stdout, colorOverride = oldOut, oldOverride }()

	out := runPs(t, map[string]*config.Service{"api": {Command: "/x/api"}})
	if strings.Contains(out, "\033[") {
		t.Errorf("颜色关闭时不应输出 ANSI 转义码\n%s", out)
	}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// TestPsAligned 校验各列对齐：剥掉颜色码后，每行可见宽度应一致。
func TestPsAligned(t *testing.T) {
	oldOut, oldOverride := stdout, colorOverride
	stdout = &bytes.Buffer{}
	colorOverride = "always"
	defer func() { stdout, colorOverride = oldOut, oldOverride }()

	out := runPs(t, map[string]*config.Service{
		"api":         {Command: "/x/api"},
		"longer_name": {Command: "/x/longer"},
	})
	lines := strings.Split(strings.TrimSuffix(stripANSI(out), "\n"), "\n")
	if len(lines) != 3 { // 表头 + 2 个服务
		t.Fatalf("行数 = %d, want 3\n%s", len(lines), out)
	}
	w := len(lines[0])
	for i, l := range lines[1:] {
		if len(l) != w {
			t.Errorf("第 %d 行可见宽度 %d != 表头 %d\n%s", i+1, len(l), w, out)
		}
	}
}
