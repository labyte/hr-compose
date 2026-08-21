package cli

import "github.com/spf13/cobra"

var configReal bool

var configCmd = &cobra.Command{
	Use:               "config [name]",
	Short:             "校验编排文件并打印 service 内容：默认预览生成的 unit，--real 查看磁盘上的实际文件",
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
		return e.Config(name, configReal)
	},
}

func init() {
	configCmd.Flags().BoolVar(&configReal, "real", false, "查看磁盘上实际的 unit 文件（默认打印 hr-compose.yml 生成的预览）")
}
