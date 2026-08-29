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
