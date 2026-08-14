package engine

import (
	"os"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorGray   = "\033[90m"
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

// stateColor 按 ActiveState 返回颜色码；未知/未加载状态返回空串（不着色）。
func stateColor(active string) string {
	switch active {
	case "active":
		return colorGreen
	case "failed":
		return colorRed
	case "activating", "deactivating", "reloading":
		return colorYellow
	case "inactive":
		return colorGray
	}
	return ""
}
