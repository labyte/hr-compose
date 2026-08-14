package cli

import "github.com/spf13/cobra"

var stopCmd = &cobra.Command{
	Use:   "stop [name]",
	Short: "停止服务（保留 unit 与 enable），不指定则停止全部",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		return e.Stop(name)
	},
}
