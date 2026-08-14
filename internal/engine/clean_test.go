package engine

import (
	"os"
	"path/filepath"
	"testing"

	"hr.compose/internal/config"
)

func TestDownClearsJournalForJournalService(t *testing.T) {
	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"}, // 默认 std_output=journal
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

func TestClearLogsJournal(t *testing.T) {
	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	if err := New(cfg, fake).ClearLogs("api"); err != nil {
		t.Fatal(err)
	}
	if !containsAction(fake.actions, "clear-journal") {
		t.Errorf("ClearLogs(journal) 应清空 journal: %v", fake.actions)
	}
}

func TestClearLogsTruncatesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "api.log")
	if err := os.WriteFile(p, []byte("some logs\nmore logs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api", StdOutput: "file:" + p},
	}}
	if err := New(cfg, &fakeSys{}).ClearLogs("api"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() != 0 {
		t.Errorf("日志文件应被截断为 0 字节，size=%v err=%v", fi, err)
	}
}
