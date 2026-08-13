package engine

import (
	"fmt"

	"github.com/labyte/hr-compose/internal/unit"
)

// Config 打印每个服务生成的 systemd unit 内容，用于校验与调试。
func (e *Engine) Config() error {
	for _, name := range e.order() {
		g, err := unit.Generate(name, e.cfg.Services[name])
		if err != nil {
			return err
		}
		fmt.Printf("# ===== %s =====\n", g.UnitPath)
		fmt.Print(g.Content)
	}
	return nil
}
