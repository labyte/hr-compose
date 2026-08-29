package cli

import "github.com/spf13/cobra"

var enableCmd = &cobra.Command{
	Use:               "enable [name...]",
	Short:             "设置服务开机启动（仅 enable，不启停），不指定则全部",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: serviceCompletion,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		return e.Enable(args...)
	},
}
