package cli

import "github.com/spf13/cobra"

var downCmd = &cobra.Command{
	Use:               "down [name...]",
	Short:             "停止并清理指定服务（删除 unit 文件），不指定则全部",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: serviceCompletion,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		return e.Down(args...)
	},
}
