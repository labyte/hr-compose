package engine

import "fmt"

// Enable 设置服务开机启动（仅 systemctl enable，不启停、不改写 unit），name 为空则全部。
func (e *Engine) Enable(name string) error {
	names, err := e.resolve(name)
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := e.sys.Enable(e.unitName(n)); err != nil {
			return fmt.Errorf("enable %s: %w", n, err)
		}
		fmt.Printf("enable %s OK\n", n)
	}
	return nil
}

// Disable 取消服务开机启动（仅 systemctl disable，不启停、不删除 unit），name 为空则全部。
func (e *Engine) Disable(name string) error {
	names, err := e.resolve(name)
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := e.sys.Disable(e.unitName(n)); err != nil {
			return fmt.Errorf("disable %s: %w", n, err)
		}
		fmt.Printf("disable %s OK\n", n)
	}
	return nil
}
