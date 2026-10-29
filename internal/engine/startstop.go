package engine

import (
	"errors"
	"fmt"
)

// Start 启动已安装的 unit（不生成、不改写、不 enable），names 为空则全部，按依赖顺序。
// 若未先执行过 up（unit 不存在），由 systemd 报错提示。
// 与 up 共用一套复核：systemctl 返回 0 只代表任务受理，只有复核确认真的起来了才打印 OK，
// 没起来的逐条报出原因（见 notRunningReason）并以非 0 退出。
func (e *Engine) Start(names ...string) error {
	targets, err := e.resolve(names...)
	if err != nil {
		return err
	}
	return e.startAndVerify(targets, e.sys.Start, "启动", "start")
}

// Restart 重启指定服务；names 为空则重启全部。同样在收尾复核进程是否真的起来了。
func (e *Engine) Restart(names ...string) error {
	targets, err := e.resolve(names...)
	if err != nil {
		return err
	}
	return e.startAndVerify(targets, e.sys.Restart, "重启", "restart")
}

// startAndVerify 是 Start 与 Restart 的共用流程（两者只差动作与措辞）：
//   - systemctl 本身报错 → 保持既有 fail-fast，立刻停止提交其余服务（依赖没起来时后面的多半也起不来）
//   - 提交成功的服务统一复核；确实起来了的才打印 "<verb> <name> OK"，没起来的逐条报错
//   - 问题一次性汇总返回（errors.Join），不把部分成功说成成功
func (e *Engine) startAndVerify(targets []string, act func(string) error, cnVerb, verb string) error {
	var errs []error
	var submitted []string
	for _, n := range targets {
		if err := act(e.unitName(n)); err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", cnVerb, n, err))
			break // fail-fast：与旧行为一致，systemctl 直接报错时不再继续提交
		}
		submitted = append(submitted, n)
	}
	verrs, started := e.verifySubmitted(submitted)
	errs = append(errs, verrs...)
	// OK 行走包级 stdout 写入口（生产即 os.Stdout，与 ps 表格同一个），
	// 让"只有复核通过才打印 OK"这条契约能被测试断言，而不只能靠人眼看。
	for _, n := range started {
		fmt.Fprintf(stdout, "%s %s OK\n", verb, n)
	}
	return errors.Join(errs...)
}

// Stop 停止服务（保留 unit 文件与 enable 状态，不删除），names 为空则全部，按依赖逆序。
// systemctl stop 会等作业结束、失败即返回非 0，语义上不需要额外复核。
func (e *Engine) Stop(names ...string) error {
	targets, err := e.resolve(names...)
	if err != nil {
		return err
	}
	for i := len(targets) - 1; i >= 0; i-- {
		n := targets[i]
		if err := e.sys.Stop(e.unitName(n)); err != nil {
			return fmt.Errorf("停止 %s: %w", n, err)
		}
		fmt.Fprintf(stdout, "stop %s OK\n", n)
	}
	return nil
}
