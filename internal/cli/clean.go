package cli

import "github.com/spf13/cobra"

var cleanYes bool

var cleanCmd = &cobra.Command{
	Use:               "clean [name...]",
	Short:             "清除服务日志（journal 清空 / file 截断），不指定则全部",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: serviceCompletion,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		return e.ClearLogs(cleanYes, args...)
	},
}

// -y 归 clean 专用：清 journal 会清空整个系统 journal，交互环境可在提示处回答，
// 脚本/CI 没有提示处，必须显式 --yes/-y 才执行 journal 清理，否则跳过。
// 简写不与 logs 的 -f 冲突（cobra 合并子命令标志集时同简写会 panic，见 TestLogsFlagShorthandNoConflict）。
func init() {
	cleanCmd.Flags().BoolVarP(&cleanYes, "yes", "y", false,
		"确认清空整个系统 journal（仅目标含 journal 服务时需要；交互环境可在提示处回答代替）")
}
