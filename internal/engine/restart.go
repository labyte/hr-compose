package engine

import "fmt"

// Restart 重启指定服务；name 为空则重启全部。
func (e *Engine) Restart(name string) error {
	names, err := e.resolve(name)
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := e.sys.Restart(n + ".service"); err != nil {
			return fmt.Errorf("重启 %s: %w", n, err)
		}
		fmt.Printf("restart %s OK\n", n)
	}
	return nil
}
