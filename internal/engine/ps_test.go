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

func TestMergedState(t *testing.T) {
	cases := []struct{ active, sub, want string }{
		{"active", "running", "active"},              // 运行中默认子状态，省略
		{"active", "", "active"},                     // 空子状态
		{"active", "exited", "active/exited"},        // oneshot 已完成，保留两级
		{"active", "waiting", "active/waiting"},      // notify 等待
		{"inactive", "dead", "inactive"},             // 停止默认子状态，省略
		{"failed", "failed", "failed"},               // 子状态与主状态相同，省略
		{"activating", "start", "activating/start"},  // 启动中
		{"activating", "auto-restart", "restarting"}, // 自动重启中，单独表达
		{"deactivating", "stop", "deactivating"},
		{"deactivating", "stop-sigterm", "deactivating/stop-sigterm"},
		{"reloading", "reload", "reloading"},
	}
	for _, tc := range cases {
		if got := mergedState(tc.active, tc.sub); got != tc.want {
			t.Errorf("mergedState(%q, %q) = %q, want %q", tc.active, tc.sub, got, tc.want)
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

	// fakeSys.Show 返回 active/running/enabled/123/1048576 + fragment 路径 + 单调启动时刻；
	// 期望 UPTIME=50m、CONFIG=FragmentPath、description 都出现在表中
	out := runPs(t, map[string]*config.Service{
		"api": {Command: "/x/api", Description: "主业务 API"},
	})
	for _, want := range []string{
		"NAME", "STATUS", "ENABLED", "PID", "MEMORY", "UPTIME", "CONFIG", "DESCRIPTION",
		"api", "active", "enabled", "1.0M", "50m", "/etc/systemd/system/api.service", "主业务 API",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应包含 %q\n%s", want, out)
		}
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
	if !strings.Contains(out, "active") {
		t.Errorf("redis 缺失于批量结果时应通过 Show 回退取到 active\n%s", out)
	}
}
