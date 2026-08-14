package engine

import (
	"bytes"
	"strings"
	"testing"

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

func TestFormatBytes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "0B"},
		{"512", "512B"},
		{"1024", "1K"},
		{"1048576", "1.0M"},
		{"1073741824", "1.0G"},
		{"18446744073709551615", "-"}, // systemd 未统计哨兵值
		{"-", "-"},                    // 非数值透传
		{"10M", "10M"},                // 已格式化透传
	}
	for _, tc := range cases {
		if got := formatBytes(tc.in); got != tc.want {
			t.Errorf("formatBytes(%q) = %q, want %q", tc.in, got, tc.want)
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
	want := text.Colors{text.FgGreen}.Sprint("active")
	if !strings.Contains(out, want) {
		t.Errorf("active 状态应染绿（含 %q）\n%s", want, out)
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

	// fakeSys.Show 返回 active/running/enabled/123/1048576；description 应出现在表中
	out := runPs(t, map[string]*config.Service{
		"api": {Command: "/x/api", Description: "主业务 API"},
	})
	for _, want := range []string{"NAME", "ACTIVE", "SUB", "ENABLED", "DESCRIPTION", "api", "enabled", "1.0M", "主业务 API"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应包含 %q\n%s", want, out)
		}
	}
}
