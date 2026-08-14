package cli

import "github.com/spf13/cobra"

var enableCmd = &cobra.Command{
	Use:   "enable [name]",
	Short: "设置服务开机启动（仅 enable，不启停），不指定则全部",
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
		return e.Enable(name)
	},
}
