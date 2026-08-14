package cli

import "github.com/spf13/cobra"

var configCmd = &cobra.Command{
	Use:               "config [name]",
	Short:             "校验编排文件并打印生成的 service 内容，可指定服务",
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
		return e.Config(name)
	},
}
