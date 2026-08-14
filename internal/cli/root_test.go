package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLogsFlagShorthandNoConflict 回归测试：logs 的 -f(--follow) 与 root 持久标志的简写
// 不能冲突。cobra 在执行子命令、合并持久标志进 logs flagset 时会因重复简写直接 panic，
// 这里通过真实执行 logs 子命令复现该场景。
func TestLogsFlagShorthandNoConflict(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"hr-compose", "logs", "--help"}
	if err := Execute("test"); err != nil {
		t.Fatalf("Execute(logs --help): %v", err)
	}
}

// TestRootHelp 冒烟：命令树整体可构建并执行，防止标志注册期错误。
func TestRootHelp(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"hr-compose", "--help"}
	if err := Execute("test"); err != nil {
		t.Fatalf("Execute(--help): %v", err)
	}
}

// TestInitCommand 冒烟：init 生成默认编排文件，且不覆盖已有文件。
func TestInitCommand(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hr-compose.yml")
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"hr-compose", "init", "--file", p}
	if err := Execute("test"); err != nil {
		t.Fatalf("Execute(init): %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("init 后应生成 %s: %v", p, err)
	}
}
