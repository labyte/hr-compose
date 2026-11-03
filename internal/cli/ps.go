package cli

import "github.com/spf13/cobra"

var psWatch bool

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "列出服务状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		if psWatch {
			return e.WatchPs()
		}
		return e.Ps()
	},
}

func init() {
	psCmd.Flags().BoolVarP(&psWatch, "watch", "w", false, "实时刷新状态表（Ctrl+C 退出）")
}
