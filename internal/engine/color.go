package engine

import (
	"os"

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
