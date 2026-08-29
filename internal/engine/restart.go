package engine

import "fmt"

// Restart 重启指定服务；names 为空则重启全部。
func (e *Engine) Restart(names ...string) error {
	targets, err := e.resolve(names...)
	if err != nil {
		return err
	}
	for _, n := range targets {
		if err := e.sys.Restart(e.unitName(n)); err != nil {
			return fmt.Errorf("重启 %s: %w", n, err)
		}
		fmt.Printf("restart %s OK\n", n)
	}
	return nil
}
