package cli

import "github.com/spf13/cobra"

var restartCmd = &cobra.Command{
	Use:   "restart [name]",
	Short: "重启服务，不指定则重启全部",
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
		return e.Restart(name)
	},
}
