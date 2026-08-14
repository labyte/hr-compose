package cli

import (
	"github.com/spf13/cobra"

	"hr.compose/internal/config"
)

// serviceCompletion 提供服务名补全：从编排文件加载已定义的服务名，供各 [name] 子命令使用。
func serviceCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	path := cfgFile
	if path == "" {
		path = config.DefaultPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		// 编排文件缺失/非法时不做补全
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(cfg.Services))
	for n := range cfg.Services {
		names = append(names, n)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
