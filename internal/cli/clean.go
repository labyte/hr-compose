package cli

import "github.com/spf13/cobra"

var cleanCmd = &cobra.Command{
	Use:               "clean [name]",
	Short:             "清除服务日志（journal 清空 / file 截断），不指定则全部",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: serviceCompletion,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		return e.ClearLogs(name)
	},
}
