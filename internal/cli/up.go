package cli

import "github.com/spf13/cobra"

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "生成并启动全部服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		if err := e.Up(); err != nil {
			return err
		}
		// up 完成后直接展示全部服务状态
		return e.Ps()
	},
}
