package engine

import (
	"fmt"
	"os"
	"text/tabwriter"
)

// Ps 遍历 yml 服务，逐个读取 systemd 状态并格式化输出。
func (e *Engine) Ps() error {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tACTIVE\tSUB\tPID\tMEMORY")
	for _, name := range e.order() {
		fields, err := e.sys.Show(name + ".service")
		if err != nil {
			// unit 未加载（未启动过或已 down）时按空状态展示
			fmt.Fprintf(w, "%s\t-\t-\t-\t-\n", name)
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			name,
			fields["ActiveState"],
			fields["SubState"],
			fields["MainPID"],
			fields["MemoryCurrent"],
		)
	}
	return w.Flush()
}
