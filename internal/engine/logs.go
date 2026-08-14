package engine

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"hr.compose/internal/config"
)

// Logs 按服务 std_output 分发查看命令：
//   - journal    → 执行 journalctl
//   - file:<p> / append:<p> → 提示用 tail -f 查看
//   - null       → 提示用 tail 查看业务日志
func (e *Engine) Logs(name string, follow bool) error {
	names, err := e.resolve(name)
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := dispatchLogs(n, e.cfg.Services[n], follow); err != nil {
			return err
		}
	}
	return nil
}

func dispatchLogs(name string, svc *config.Service, follow bool) error {
	switch out := svc.EffectiveStdOutput(); {
	case out == "journal":
		args := []string{"-u", name + ".service", "--no-pager"}
		if follow {
			args = append(args, "-f")
		}
		return runInteractive("journalctl", args...)
	case out == "null":
		if svc.LogFile != "" {
			fmt.Printf("服务 %s 日志由业务程序写入 %s，请使用：tail -f %s\n", name, svc.LogFile, svc.LogFile)
		} else {
			fmt.Printf("服务 %s 未托管日志（std_output=null），请用 tail 查看业务日志文件\n", name)
		}
		return nil
	case strings.HasPrefix(out, "file:"):
		return hintTail(name, strings.TrimPrefix(out, "file:"))
	case strings.HasPrefix(out, "append:"):
		return hintTail(name, strings.TrimPrefix(out, "append:"))
	default:
		return fmt.Errorf("服务 %s 不支持的 std_output: %q", name, out)
	}
}

func hintTail(name, path string) error {
	fmt.Printf("服务 %s 日志写入 %s，请使用：tail -f %s\n", name, path, path)
	return nil
}

// runInteractive 以继承标准输入输出方式执行外部命令（journalctl）。
func runInteractive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
