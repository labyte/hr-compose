package cli

import (
	"github.com/spf13/cobra"

	"hr.compose/internal/config"
)

var initCmd = &cobra.Command{
	Use:   "init [services-dir]",
	Short: "生成默认 hr-compose.yml 编排文件模板（指定目录时扫描其子文件夹生成服务列表）",
	Long:  "生成默认 hr-compose.yml 编排文件模板；文件已存在则不覆盖。指定 services-dir 时扫描其直接子文件夹，每个子文件夹作为一个服务生成初始配置（仅 command 与 working_dir，不含依赖关系）。",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := cfgFile
		if path == "" {
			path = config.DefaultPath()
		}
		if len(args) == 1 {
			return config.InitFromDir(args[0], path)
		}
		return config.Init(path)
	},
}
