package engine

import (
	"fmt"
	"os"
	"strings"
)

// ClearLogs 清除指定服务的日志，name 为空则全部：
//   - journal       → 清空整个系统 journal（journald 不支持按 unit 删除，多个服务只清一次）
//   - file:/append: → 截断对应日志文件
//   - null          → 日志由业务程序管理，提示自行处理
func (e *Engine) ClearLogs(name string) error {
	names, err := e.resolve(name)
	if err != nil {
		return err
	}
	cleared := false
	for _, n := range names {
		svc := e.cfg.Services[n]
		switch out := svc.EffectiveStdOutput(); {
		case out == "journal":
			if !cleared {
				if err := e.sys.ClearJournal(); err != nil {
					return fmt.Errorf("清除 %s 的 journal 日志: %w", n, err)
				}
				cleared = true
			}
			fmt.Printf("已清空 %s 的 journal 日志（整个系统 journal）\n", n)
		case out == "null":
			fmt.Printf("服务 %s 日志由业务程序管理（std_output=null），请自行清理\n", n)
		case strings.HasPrefix(out, "file:"):
			if err := truncateLog(n, strings.TrimPrefix(out, "file:")); err != nil {
				return err
			}
		case strings.HasPrefix(out, "append:"):
			if err := truncateLog(n, strings.TrimPrefix(out, "append:")); err != nil {
				return err
			}
		default:
			return fmt.Errorf("服务 %s 不支持的 std_output: %q", n, out)
		}
	}
	return nil
}

// truncateLog 截断日志文件为 0 字节；文件不存在则跳过。
func truncateLog(name, path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("服务 %s 日志文件 %s 不存在，跳过\n", name, path)
		return nil
	}
	if err := os.Truncate(path, 0); err != nil {
		return fmt.Errorf("截断 %s 的日志 %s: %w", name, path, err)
	}
	fmt.Printf("已清空 %s 的日志文件 %s\n", name, path)
	return nil
}
