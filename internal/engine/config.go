package engine

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"hr.compose/internal/unit"
)

// Config 打印生成的服务 unit 内容，用于校验与调试。name 为空则打印全部，否则只打印指定服务。
// 段头标注该 unit 将写入的完整文件路径（UnitDir + unit 文件名），与 ps 表不再展示的 CONFIG 列互补。
func (e *Engine) Config(name string) error {
	names, err := e.resolve(name)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, n := range names {
		g, err := unit.Generate(n, e.cfg.Services[n])
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "# ===== %s =====\n", filepath.Join(UnitDir, g.UnitPath))
		b.WriteString(g.Content)
	}
	_, err = io.WriteString(stdout, b.String())
	return err
}
