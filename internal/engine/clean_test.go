package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hr.compose/internal/config"
)

func TestDownClearsJournalForJournalService(t *testing.T) {
	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", StdOutput: "journal"}, // 默认已是 null，需显式 journal 才清理
	}}
	if err := New(cfg, fake).Down(); err != nil {
		t.Fatal(err)
	}
	if !containsAction(fake.actions, "clear-journal") {
		t.Errorf("存在 journal 服务时 down 应清理 journal: %v", fake.actions)
	}
}

func TestDownSkipsJournalWhenNoJournalService(t *testing.T) {
	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", StdOutput: "file:/var/log/api.log"},
	}}
	if err := New(cfg, fake).Down(); err != nil {
		t.Fatal(err)
	}
	if containsAction(fake.actions, "clear-journal") {
		t.Errorf("无 journal 服务时 down 不应清理 journal: %v", fake.actions)
	}
}

func TestDownSubsetSkipsJournalWhenNamedNotJournal(t *testing.T) {
	// down 指定服务只对目标集判断 journal：目标 api 非 journal，不应因其他服务的 journal 清空整个系统 journal
	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", StdOutput: "file:/var/log/api.log"},
		"redis": {Command: "/x/redis", StdOutput: "journal"},
	}}
	if err := New(cfg, fake).Down("api"); err != nil {
		t.Fatal(err)
	}
	if containsAction(fake.actions, "clear-journal") {
		t.Errorf("Down(api) 不应因 redis 的 journal 清空 journal: %v", fake.actions)
	}
}

func TestDownSubsetClearsJournalWhenNamedJournal(t *testing.T) {
	// 目标集内存在 journal 服务时才清理
	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", StdOutput: "journal"},
		"redis": {Command: "/x/redis", StdOutput: "file:/var/log/redis.log"},
	}}
	if err := New(cfg, fake).Down("api"); err != nil {
		t.Fatal(err)
	}
	if !containsAction(fake.actions, "clear-journal") {
		t.Errorf("Down(api) 目标含 journal 服务时应清理 journal: %v", fake.actions)
	}
}

func TestClearLogsJournal(t *testing.T) {
	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", StdOutput: "journal"},
	}}
	if err := New(cfg, fake).ClearLogs(true, "api"); err != nil {
		t.Fatal(err)
	}
	if !containsAction(fake.actions, "clear-journal") {
		t.Errorf("ClearLogs(--yes, journal) 应清空 journal: %v", fake.actions)
	}
}

// 清 journal 会抹掉整个系统 journal，非交互环境（脚本/CI）没加 --yes 时必须跳过而不是静默执行。
func TestClearLogsJournalSkippedWithoutYesInNonTTY(t *testing.T) {
	oldOut := stdout
	stdout = &bytes.Buffer{}
	defer func() { stdout = oldOut }()

	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", StdOutput: "journal"},
	}}
	if err := New(cfg, fake).ClearLogs(false, "api"); err != nil {
		t.Fatal(err)
	}
	if containsAction(fake.actions, "clear-journal") {
		t.Errorf("非交互且无 --yes 时不应清 journal: %v", fake.actions)
	}
	out := stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "已跳过") || !strings.Contains(out, "--yes") {
		t.Errorf("跳过时应说明原因与 --yes 用法:\n%s", out)
	}
}

// 交互环境按 stdin 回答决定是否清 journal：仅 y/yes 确认，其余（含空回答）一律否定。
func TestClearLogsJournalConfirmFromStdin(t *testing.T) {
	cases := []struct {
		answer string
		want   bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"n\n", false},
		{"\n", false},
	}
	for _, tc := range cases {
		oldIn, oldTTY, oldOut := stdin, stdinIsTTY, stdout
		stdin = strings.NewReader(tc.answer)
		stdinIsTTY = func() bool { return true }
		buf := &bytes.Buffer{}
		stdout = buf

		fake := &fakeSys{}
		cfg := &config.Config{Services: map[string]*config.Service{
			"api": {Command: "/x/api", StdOutput: "journal"},
		}}
		err := New(cfg, fake).ClearLogs(false, "api")

		stdin, stdinIsTTY, stdout = oldIn, oldTTY, oldOut
		if err != nil {
			t.Fatal(err)
		}
		if got := containsAction(fake.actions, "clear-journal"); got != tc.want {
			t.Errorf("回答 %q: clear-journal=%v, want %v", tc.answer, got, tc.want)
		}
		if !strings.Contains(buf.String(), "确认? [y/N]") {
			t.Errorf("交互确认应打印 [y/N] 提示:\n%s", buf.String())
		}
	}
}

func TestClearLogsTruncatesFile(t *testing.T) {
	oldOut := stdout
	stdout = &bytes.Buffer{}
	defer func() { stdout = oldOut }()

	p := filepath.Join(t.TempDir(), "api.log")
	if err := os.WriteFile(p, []byte("some logs\nmore logs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", StdOutput: "file:" + p},
	}}
	if err := New(cfg, &fakeSys{}).ClearLogs(false, "api"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() != 0 {
		t.Errorf("日志文件应被截断为 0 字节，size=%v err=%v", fi, err)
	}
}

// file: 模式 systemd 不开 O_APPEND：截断后运行中进程的 fd 偏移不归零，后续写入留 NUL 空孔，
// 必须警告重启；append: 以 O_APPEND 打开、截断安全，不警告。
func TestClearLogsFileRunningWarnsRestart(t *testing.T) {
	oldOut := stdout
	buf := &bytes.Buffer{}
	stdout = buf
	defer func() { stdout = oldOut }()

	dir := t.TempDir()
	p := filepath.Join(dir, "api.log")
	if err := os.WriteFile(p, []byte("logs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// fakeSys.Show 恒返回 active，模拟服务仍在运行
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", StdOutput: "file:" + p},
	}}
	if err := New(cfg, &fakeSys{}).ClearLogs(false, "api"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "警告") || !strings.Contains(buf.String(), "restart api") {
		t.Errorf("file: 服务运行中截断应警告重启:\n%s", buf.String())
	}

	buf.Reset()
	pa := filepath.Join(dir, "api-append.log")
	if err := os.WriteFile(pa, []byte("logs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgAppend := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", StdOutput: "append:" + pa},
	}}
	if err := New(cfgAppend, &fakeSys{}).ClearLogs(false, "api"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "警告") {
		t.Errorf("append: 截断安全，不应警告:\n%s", buf.String())
	}
}

// 状态查不到时不警告：宁可少警告一次，也不编造运行状态骗用户做不必要的重启。
func TestClearLogsFileNoWarnWhenStateUnknown(t *testing.T) {
	oldOut := stdout
	buf := &bytes.Buffer{}
	stdout = buf
	defer func() { stdout = oldOut }()

	p := filepath.Join(t.TempDir(), "api.log")
	if err := os.WriteFile(p, []byte("logs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", StdOutput: "file:" + p},
	}}
	if err := New(cfg, &showFailSys{}).ClearLogs(false, "api"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "警告") {
		t.Errorf("状态查不到时不应警告:\n%s", buf.String())
	}
}

// 单个服务清理失败不中断其余服务：api 的"日志"是目录（截断必败），web 的文件仍应被截断，
// 错误汇总返回。
func TestClearLogsAggregatesErrors(t *testing.T) {
	oldOut := stdout
	stdout = &bytes.Buffer{}
	defer func() { stdout = oldOut }()

	dir := t.TempDir()
	webLog := filepath.Join(dir, "web.log")
	if err := os.WriteFile(webLog, []byte("logs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", StdOutput: "file:" + dir}, // 目录截断失败
		"web": {Command: "/x/web", StdOutput: "file:" + webLog},
	}}
	err := New(cfg, &fakeSys{}).ClearLogs(false, "api", "web")
	if err == nil || !strings.Contains(err.Error(), "api") {
		t.Errorf("api 截断失败应汇总报错, err=%v", err)
	}
	if fi, statErr := os.Stat(webLog); statErr != nil || fi.Size() != 0 {
		t.Errorf("api 失败不应中断 web 的清理，size=%v err=%v", fi, statErr)
	}
}
