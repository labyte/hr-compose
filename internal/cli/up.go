package cli

import "github.com/spf13/cobra"

var upCmd = &cobra.Command{
	Use:               "up [name...]",
	Short:             "生成并启动服务（指定名字时自动带上其依赖），不指定则全部",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: serviceCompletion,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		if err := e.Up(args...); err != nil {
			return err
		}
		// up 完成后直接展示服务状态
		return e.Ps()
	},
}
