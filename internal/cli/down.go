package cli

import "github.com/spf13/cobra"

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "停止并清理全部服务（删除 unit 文件）",
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		return e.Down()
	},
}
