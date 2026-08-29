package cli

import "github.com/spf13/cobra"

var disableCmd = &cobra.Command{
	Use:               "disable [name...]",
	Short:             "取消服务开机启动（仅 disable，不删 unit），不指定则全部",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: serviceCompletion,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		return e.Disable(args...)
	},
}
