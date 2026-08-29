package engine

import "fmt"

// Start 启动已安装的 unit（不生成、不改写、不 enable），names 为空则全部，按依赖顺序。
// 若未先执行过 up（unit 不存在），由 systemd 报错提示。
func (e *Engine) Start(names ...string) error {
	targets, err := e.resolve(names...)
	if err != nil {
		return err
	}
	for _, n := range targets {
		if err := e.sys.Start(e.unitName(n)); err != nil {
			return fmt.Errorf("启动 %s: %w", n, err)
		}
		fmt.Printf("start %s OK\n", n)
	}
	return nil
}

// Stop 停止服务（保留 unit 文件与 enable 状态，不删除），names 为空则全部，按依赖逆序。
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
		fmt.Printf("stop %s OK\n", n)
	}
	return nil
}
