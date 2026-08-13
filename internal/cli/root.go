// Package cli 定义 cobra 命令树与全局参数。
package cli

import (
	"github.com/spf13/cobra"

	"github.com/labyte/hr-compose/internal/config"
	"github.com/labyte/hr-compose/internal/engine"
	"github.com/labyte/hr-compose/internal/systemctl"
)

var cfgFile string

// Execute 执行命令树，返回错误给 main 处理。
func Execute(version string) error {
	rootCmd.Version = version
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "file", "f", "", "编排文件路径（默认当前目录 hr-compose.yml）")
	rootCmd.AddCommand(upCmd, downCmd, psCmd, restartCmd, logsCmd, configCmd)
}

var rootCmd = &cobra.Command{
	Use:           "hr-compose",
	Short:         "对标 docker-compose 的本地服务编排工具",
	Long:          "hr-compose 读取 hr-compose.yml，基于 systemd 管理 Linux 上的多个业务服务。",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// load 读取编排文件并构造引擎。
func load() (*engine.Engine, error) {
	path := cfgFile
	if path == "" {
		path = config.DefaultPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return engine.New(cfg, systemctl.New()), nil
}
