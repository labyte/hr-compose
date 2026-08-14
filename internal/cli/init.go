package cli

import (
	"github.com/spf13/cobra"

	"hr.compose/internal/config"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "生成默认 hr-compose.yml 编排文件模板",
	Long:  "生成默认 hr-compose.yml 编排文件模板；文件已存在则不覆盖。",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := cfgFile
		if path == "" {
			path = config.DefaultPath()
		}
		return config.Init(path)
	},
}
