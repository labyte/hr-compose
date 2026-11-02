package engine

import (
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/text"
)

// colorOverride 测试用颜色强制开关：""（自动）、"always"、"never"。
var colorOverride = ""

// colorsOn 判断是否输出 ANSI 颜色：
//   - colorOverride 优先（测试用）
//   - NO_COLOR 环境变量非空即关闭（https://no-color.org）
//   - 否则仅在 stdout 是终端（字符设备）时输出，管道/重定向自动无色
func colorsOn() bool {
	switch colorOverride {
	case "always":
		return true
	case "never":
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// stateColors 按 ActiveState 返回 go-pretty 颜色；未知/未加载状态返回 nil（不着色）。
func stateColors(active string) text.Colors {
	switch active {
	case "active":
		return text.Colors{text.FgGreen}
	case "failed":
		return text.Colors{text.FgRed}
	case "activating", "deactivating", "reloading":
		return text.Colors{text.FgYellow}
	case "inactive":
		return text.Colors{text.FgHiBlack}
	}
	return nil
}

// borderChars 是表格边框字符集（go-pretty StyleLight 用到的全部制表符）。
const borderChars = "─│┌┐└┘├┤┬┴┼"

// syncTextColors 把 colorsOn 的判断同步给 go-pretty 的 text 包：text 包在 init 时按环境
// （TERM=dumb、NO_COLOR 等）全局决定颜色开关，测试沙箱/CI 里会把 Colors.Sprint 变成 no-op，
// 使 colorOverride=always 名不副实、断言落空。渲染前同步一次，颜色开关就以我们自己的判断为准。
func syncTextColors(on bool) {
	if on {
		text.EnableColors()
	} else {
		text.DisableColors()
	}
}

// borderColors 是边框颜色：暗灰（亮黑）。终端里制表符的笔画粗细由字体决定、代码改不了，
// 让线条"更细更暗"只能降颜色亮度——暗灰线条视觉上比亮白细一圈、也退到背景里，
// 数据单元格因此更突出；与 stateColors 里 inactive 的暗灰同一档，整表色调统一。
var borderColors = text.Colors{text.FgHiBlack}

// dimBorders 把渲染结果 s 里的边框字符（连续成段地）包上 borderColors，数据单元格原样不动。
// on=false（管道/重定向/NO_COLOR）时原样返回，不给非终端输出塞转义码。
func dimBorders(s string, on bool) string {
	if !on || s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len("\x1b[90m\x1b[0m"))
	runStart := -1 // 当前边框字符段的起始字节下标，-1 表示不在段内
	for i, r := range s {
		if strings.ContainsRune(borderChars, r) {
			if runStart < 0 {
				runStart = i
			}
			continue
		}
		if runStart >= 0 {
			b.WriteString(borderColors.Sprint(s[runStart:i]))
			runStart = -1
		}
		b.WriteRune(r)
	}
	if runStart >= 0 {
		b.WriteString(borderColors.Sprint(s[runStart:]))
	}
	return b.String()
}
