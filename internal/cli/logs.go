package cli

import "github.com/spf13/cobra"

var follow bool

var logsCmd = &cobra.Command{
	Use:               "logs [name]",
	Short:             "查看服务日志",
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
		return e.Logs(name, follow)
	},
}

func init() {
	logsCmd.Flags().BoolVarP(&follow, "follow", "f", false, "实时跟踪日志")
}
