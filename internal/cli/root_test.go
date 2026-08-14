package cli

import (
	"os"
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
