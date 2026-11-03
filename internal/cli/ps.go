package cli

import (
	"time"

	"github.com/spf13/cobra"

	"hr.compose/internal/engine"
)

var psWatch bool
var psInterval time.Duration

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "列出服务状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		if psWatch {
			return e.WatchPs(psInterval)
		}
		return e.Ps()
	},
}

func init() {
	psCmd.Flags().BoolVarP(&psWatch, "watch", "w", false, "实时刷新状态表（Ctrl+C 退出）")
	psCmd.Flags().DurationVarP(&psInterval, "interval", "i", engine.DefaultWatchInterval,
		"刷新间隔（如 500ms、1s、5s），仅配合 --watch 生效")
}
