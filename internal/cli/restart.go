package cli

import "github.com/spf13/cobra"

var restartCmd = &cobra.Command{
	Use:               "restart [name...]",
	Short:             "重启服务，不指定则重启全部",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: serviceCompletion,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		return e.Restart(args...)
	},
}
