package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hr.compose/internal/config"
)

// glob 形式的 log_file 必须解析到 mtime 最新的文件：NLog 一类业务库按日期切文件，
// 文件名里的日期旧不代表 mtime 旧，以 mtime 为准才能落到当天在写的那个。
func TestResolveLogFileGlobPicksLatestMtime(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "app-2026-11-01.log")
	today := filepath.Join(dir, "app-2026-11-02.log")
	for _, p := range []string{old, today} {
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 名字新的文件 mtime 反而旧：验证按 mtime 而非文件名排序
	yesterday := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(today, yesterday, yesterday); err != nil {
		t.Fatal(err)
	}
	if got, ok := resolveLogFile(filepath.Join(dir, "app-*.log")); !ok || got != old {
		t.Errorf("glob 应取 mtime 最新者 = %s, got=%q ok=%v", old, got, ok)
	}

	// 不含通配符原样返回，不检查存在性
	p := filepath.Join(dir, "plain.log")
	if got, ok := resolveLogFile(p); !ok || got != p {
		t.Errorf("无通配符应原样返回: got=%q ok=%v", got, ok)
	}
	// glob 无匹配返回 ok=false
	if _, ok := resolveLogFile(filepath.Join(dir, "nomatch-*.log")); ok {
		t.Errorf("glob 无匹配应 ok=false")
	}
}

// mtime 相同（秒级粒度文件系统、同一瞬间创建）时平局落到文件名排序靠后者：
// glob 升序 + ISO 日期文件名字典序即时间序，平局应取日期新者而非旧者。
func TestResolveLogFileGlobTieBreaksByName(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "app-2026-11-01.log")
	b := filepath.Join(dir, "app-2026-11-02.log")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	same := time.Now()
	for _, p := range []string{a, b} {
		if err := os.Chtimes(p, same, same); err != nil {
			t.Fatal(err)
		}
	}
	if got, ok := resolveLogFile(filepath.Join(dir, "app-*.log")); !ok || got != b {
		t.Errorf("mtime 平局应取文件名靠后者 %s, got=%q ok=%v", b, got, ok)
	}
}

// std_output=null 且配了 log_file 时 logs 应直接 tail 解析后的业务日志，而不是只打印提示。
func TestLogsNullWithLogFileTailsResolved(t *testing.T) {
	oldOut := stdout
	buf := &bytes.Buffer{}
	stdout = buf
	defer func() { stdout = oldOut }()

	dir := t.TempDir()
	today := filepath.Join(dir, "app-2026-11-02.log")
	if err := os.WriteFile(today, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", LogFile: filepath.Join(dir, "app-*.log")},
	}}
	if err := New(cfg, &fakeSys{}).Logs(nil, false); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, today) {
		t.Errorf("应打印解析到的业务日志文件 %s:\n%s", today, out)
	}
}

func TestLogsNullWithoutLogFileStillHints(t *testing.T) {
	oldOut := stdout
	buf := &bytes.Buffer{}
	stdout = buf
	defer func() { stdout = oldOut }()

	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	if err := New(cfg, &fakeSys{}).Logs(nil, false); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "未托管日志") {
		t.Errorf("未配 log_file 时应提示自行 tail:\n%s", out)
	}
}

func TestLogsNullLogFileGlobNoMatch(t *testing.T) {
	oldOut := stdout
	buf := &bytes.Buffer{}
	stdout = buf
	defer func() { stdout = oldOut }()

	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", LogFile: filepath.Join(t.TempDir(), "app-*.log")},
	}}
	if err := New(cfg, &fakeSys{}).Logs(nil, false); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "暂无匹配文件") {
		t.Errorf("glob 无匹配时应提示稍后重试:\n%s", out)
	}
}
