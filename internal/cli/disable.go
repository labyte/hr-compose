package cli

import "github.com/spf13/cobra"

var disableCmd = &cobra.Command{
	Use:   "disable [name]",
	Short: "取消服务开机启动（仅 disable，不删 unit），不指定则全部",
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
		return e.Disable(name)
	},
}
