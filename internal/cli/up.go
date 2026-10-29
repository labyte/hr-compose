package cli

import "github.com/spf13/cobra"

var upCmd = &cobra.Command{
	Use:               "up [name...]",
	Short:             "生成并启动服务（指定名字时自动带上其依赖），不指定则全部",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: serviceCompletion,
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := load()
		if err != nil {
			return err
		}
		// Up 自身在收尾会展示本次目标服务的状态（部分失败时也展示，failed/exited 一眼可见），
		// 所以这里不再另调 Ps；失败的服务已汇总进返回的 error，交给 cobra 打印并置非 0 退出码。
		return e.Up(args...)
	},
}
