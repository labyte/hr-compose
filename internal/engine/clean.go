package engine

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// stdin 与交互判断为包级变量，便于测试覆盖（同 stdout 变量的模式）：
// journal 清理确认在交互环境下从 stdin 读一行；非交互环境必须 --yes 显式确认。
var stdin io.Reader = os.Stdin

// stdinIsTTY 判断 stdin 是否真终端。不能用 ModeCharDevice：/dev/null 也是字符设备，
// 会把非交互误判成交互、平白打印确认提示；ioctl TIOCGWINSZ 只有真终端才成功
// （管道、/dev/null、普通文件都 ENOTTY），与 ps 读终端列宽同一手法，linux/darwin 通用。
// 脚本/CI 里 stdin 不是终端，此时清 journal 必须显式 --yes，否则跳过——
// 避免一条命令静默抹掉全系统 journal。
var stdinIsTTY = func() bool {
	type winsize struct{ rows, cols, xpix, ypix uint16 }
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	return errno == 0
}

// ClearLogs 清除指定服务的日志，names 为空则全部：
//   - journal       → 清空整个系统 journal（journald 不支持按 unit 删除，多个 journal 服务只清一次）；
//     破坏面超出单个服务，故需确认：交互环境提示回答，非交互环境必须传 yes（即 --yes），否则跳过
//   - file:/append: → 截断对应日志文件；file: 服务仍在运行时额外警告重启（fd 偏移不随截断归零，会留空孔）
//   - null          → 日志由业务程序管理，提示自行处理
//
// 单个服务清理失败不中断其余服务，错误汇总返回（同 up 的哲学）。
func (e *Engine) ClearLogs(yes bool, names ...string) error {
	targets, err := e.resolve(names...)
	if err != nil {
		return err
	}
	var (
		cleared  bool // 本次是否已清过 journal，多个 journal 服务只清一次
		prompted bool // 本次是否已问过确认，只问一次
		allowed  = yes
		errs     []error
	)
	for _, n := range targets {
		svc := e.cfg.Services[n]
		switch out := svc.EffectiveStdOutput(); {
		case out == "journal":
			if !prompted {
				prompted = true
				allowed = yes || confirmJournalClear()
			}
			if !allowed {
				fmt.Fprintf(stdout, "已跳过 %s 的 journal 清理（会清空整个系统 journal，加 --yes 确认）\n", n)
				continue
			}
			if !cleared {
				if err := e.sys.ClearJournal(); err != nil {
					errs = append(errs, fmt.Errorf("清除 %s 的 journal 日志: %w", n, err))
					continue
				}
				cleared = true
			}
			fmt.Fprintf(stdout, "已清空 %s 的 journal 日志（整个系统 journal）\n", n)
		case out == "null":
			fmt.Fprintf(stdout, "服务 %s 日志由业务程序管理（std_output=null），请自行清理\n", n)
		case strings.HasPrefix(out, "file:"):
			if err := e.truncateLog(n, strings.TrimPrefix(out, "file:"), false); err != nil {
				errs = append(errs, err)
			}
		case strings.HasPrefix(out, "append:"):
			if err := e.truncateLog(n, strings.TrimPrefix(out, "append:"), true); err != nil {
				errs = append(errs, err)
			}
		default:
			errs = append(errs, fmt.Errorf("服务 %s 不支持的 std_output: %q", n, out))
		}
	}
	return errors.Join(errs...)
}

// confirmJournalClear 交互确认清空整个系统 journal：非交互环境直接返回 false
// （脚本/CI 必须显式 --yes）；交互环境从 stdin 读一行，仅 y/yes 算确认，其余一律按否定。
func confirmJournalClear() bool {
	if !stdinIsTTY() {
		fmt.Fprintln(stdout, "检测到非交互环境，跳过清空系统 journal（确需清空请加 --yes）")
		return false
	}
	fmt.Fprint(stdout, "将清空整个系统 journal（含内核与其他服务的日志，非仅 hr-compose 服务），确认? [y/N]: ")
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// truncateLog 截断日志文件为 0 字节；文件不存在则跳过。
// appendMode=false 对应 std_output 的 file:：systemd 不开 O_APPEND 打开它，业务进程 fd 的写偏移
// 不随截断归零，后续写入会落在旧偏移处、日志前部留下大段 NUL 空孔，故服务仍在运行时警告重启一次；
// append: 以 O_APPEND 打开、每次写重算偏移，截断安全，不警告。
func (e *Engine) truncateLog(name, path string, appendMode bool) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Fprintf(stdout, "服务 %s 日志文件 %s 不存在，跳过\n", name, path)
		return nil
	}
	if err := os.Truncate(path, 0); err != nil {
		return fmt.Errorf("截断 %s 的日志 %s: %w", name, path, err)
	}
	fmt.Fprintf(stdout, "已清空 %s 的日志文件 %s\n", name, path)
	if !appendMode && e.serviceRunning(name) {
		fmt.Fprintf(stdout, "警告: %s 仍在运行，file: 模式的写偏移不随截断归零，后续日志会出现空孔，建议执行 hr-compose restart %s\n", name, name)
	}
	return nil
}

// serviceRunning 查询服务当前状态，仅 active 视为运行中。查询失败（systemctl 不可用等）
// 按未运行处理：宁可少警告一次，也不编造运行状态骗用户做不必要的重启。
func (e *Engine) serviceRunning(name string) bool {
	fields, err := e.sys.Show(e.unitName(name))
	if err != nil {
		return false
	}
	return fields["ActiveState"] == "active"
}
