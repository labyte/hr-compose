package engine

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// stdout 可被测试覆盖以捕获输出（同 UnitDir 变量的模式）。
var stdout io.Writer = os.Stdout

// Ps 遍历 yml 服务，逐个读取 systemd 状态并格式化输出。
// 终端下 ACTIVE / SUB 列按状态着色（active 绿 / failed 红 / activating 黄 / inactive 灰）。
func (e *Engine) Ps() error {
	header := []string{"NAME", "ACTIVE", "SUB", "PID", "MEMORY"}
	rows := [][]string{header}
	for _, name := range e.order() {
		fields, err := e.sys.Show(name + ".service")
		if err != nil {
			// unit 未加载（未启动过或已 down）时按空状态展示
			rows = append(rows, []string{name, "-", "-", "-", "-"})
			continue
		}
		rows = append(rows, []string{
			name,
			fields["ActiveState"],
			fields["SubState"],
			fields["MainPID"],
			fields["MemoryCurrent"],
		})
	}

	// 按纯文本计算各列宽度，避免 ANSI 转义码干扰对齐
	widths := make([]int, len(header))
	for _, r := range rows {
		for i, c := range r {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}

	on := colorsOn()
	var b strings.Builder
	for _, r := range rows {
		for i, c := range r {
			// 先填充到固定宽度，再包裹颜色码（颜色不参与宽度计算）
			cell := fmt.Sprintf("%-*s", widths[i]+1, c)
			if on && (i == 1 || i == 2) {
				if color := stateColor(r[1]); color != "" {
					cell = color + cell + colorReset
				}
			}
			b.WriteString(cell)
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(stdout, b.String())
	return err
}
