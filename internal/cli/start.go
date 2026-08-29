package cli

import "github.com/spf13/cobra"

var startCmd = &cobra.Command{
	Use:               "start [name...]",
	Short:             "启动服务，不指定则启动全部（需先 up 安装 unit）",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: serviceCompletion,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		return e.Start(args...)
	},
}
