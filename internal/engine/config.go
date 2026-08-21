package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hr.compose/internal/unit"
)

// Config 打印服务 unit 内容，用于校验与调试。name 为空则打印全部，否则只打印指定服务。
// real 为 false 时打印由 hr-compose.yml 生成的预览（段头标注"预览"及将写入的完整路径）；
// real 为 true 时读取并打印磁盘上实际的 unit 文件（段头标注"实际文件"），文件不存在时给出提示而非报错。
func (e *Engine) Config(name string, real bool) error {
	names, err := e.resolve(name)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, n := range names {
		path := filepath.Join(UnitDir, e.unitName(n))
		if real {
			content, err := os.ReadFile(path)
			switch {
			case os.IsNotExist(err):
				fmt.Fprintf(&b, "# ===== 实际文件: %s =====\n", path)
				b.WriteString("# 文件不存在：服务尚未安装（up 后生成）或已被 down\n")
			case err != nil:
				return err
			default:
				fmt.Fprintf(&b, "# ===== 实际文件: %s =====\n", path)
				b.Write(content)
			}
			continue
		}
		g, err := unit.Generate(n, e.cfg.Services[n], e.cfg.Name)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "# ===== 预览: %s =====\n", path)
		b.WriteString(g.Content)
	}
	_, err = io.WriteString(stdout, b.String())
	return err
}
