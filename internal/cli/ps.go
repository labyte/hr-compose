package cli

import "github.com/spf13/cobra"

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "列出服务状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		return e.Ps()
	},
}
