package engine

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// stdout 可被测试覆盖以捕获输出（同 UnitDir 变量的模式）。
var stdout io.Writer = os.Stdout

// Ps 遍历 yml 服务，逐个读取 systemd 状态并格式化输出。
// 列：NAME / ACTIVE / SUB / ENABLED / PID / MEMORY / DESCRIPTION，终端下状态列着色。
// ACTIVE=ActiveState（生命周期总状态），SUB=SubState（动作子状态），ENABLED=UnitFileState（是否开机启动）。
func (e *Engine) Ps() error {
	t := table.NewWriter()
	t.SetOutputMirror(stdout)
	t.AppendHeader(table.Row{"NAME", "ACTIVE", "SUB", "ENABLED", "PID", "MEMORY", "DESCRIPTION"})
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 7, WidthMax: 40}, // 描述列限宽（默认 WidthMaxEnforcer=text.WrapText 换行）
	})
	t.SetStyle(table.StyleLight)

	names := e.order()
	unitNames := make([]string, len(names))
	for i, n := range names {
		unitNames[i] = n + ".service"
	}
	// 一次批量查询所有 unit，减少进程启动
	all, err := e.sys.ShowMany(unitNames)
	if err != nil {
		all = map[string]map[string]string{}
	}
	// 批量结果中缺失的 unit（未加载）回退逐服务查询
	for _, n := range names {
		u := n + ".service"
		if _, ok := all[u]; ok {
			continue
		}
		if f, serr := e.sys.Show(u); serr == nil {
			all[u] = f
		}
	}

	on := colorsOn()
	for _, name := range names {
		svc := e.cfg.Services[name]
		fields, ok := all[name+".service"]
		if !ok {
			// unit 未加载（未启动过或已 down）时按空状态展示
			t.AppendRow(table.Row{name, "-", "-", "-", "-", "-", valueOrDash(svc.Description)})
			continue
		}
		var colors text.Colors
		if on {
			colors = stateColors(fields["ActiveState"])
		}
		// go-pretty 计算列宽时会剥离 ANSI 转义，可安全用 Sprint 生成的彩色字符串作为单元格
		t.AppendRow(table.Row{
			name,
			colors.Sprint(fields["ActiveState"]),
			colors.Sprint(fields["SubState"]),
			valueOrDash(fields["UnitFileState"]),
			fields["MainPID"],
			formatBytes(fields["MemoryCurrent"]),
			valueOrDash(svc.Description),
		})
	}
	t.Render()
	return nil
}

// valueOrDash 空值显示 "-"。
func valueOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// formatBytes 把字节数格式化为友好单位（K/M/G）；非数值或 systemd 未统计哨兵值原样处理。
func formatBytes(s string) string {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return s // 非数值（"-" 或已格式化）原样返回
	}
	// systemd 未启用内存统计时 MemoryCurrent 为 2^64-1 哨兵值
	if n >= 1<<50 {
		return "-"
	}
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fK", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
