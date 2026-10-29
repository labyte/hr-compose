package engine

import (
	"strconv"
	"strings"
	"time"
)

// sleepFunc 是复核等待的实现，变量形式便于测试覆盖（单测里 systemctl 是 fake，
// 状态即时给定，真等只会拖慢测试而不改变结论）。
var sleepFunc = time.Sleep

const (
	// upVerifyInterval 是 up 收尾复核的轮询间隔。`systemctl start` 对 Type=simple 只要 fork 成功
	// 就返回 0，ExecStart 不存在这类失败要等 exec 报错才在状态里显形，故第一轮无条件等一次。
	upVerifyInterval = 300 * time.Millisecond
	// upVerifyRounds 是最多轮询次数。正常服务第一轮就是 running；只有仍处在"启动过程中"的服务
	// （如配了 start_delay，systemd 停在 activating/start-pre）会继续等，最多 5 轮（≈1.5s）
	// 后按当时的状态给结论，不无限期挂住命令。
	upVerifyRounds = 5
)

// checkUp 复核 up 的启动结果，返回展示用的状态数据与"没起来"的服务及原因。
// 单独存在是因为 `systemctl start` 返回 0 只代表 systemd 受理了任务，进程到底活没活要读状态才知道：
// 真机实测 command 路径不存在时 start 照样返回 0（Restart=always 使其进入 auto-restart 循环），
// 不复核就会把永远起不来的服务报成成功。状态数据顺带交给 renderPs，不重复查 systemctl。
func (e *Engine) checkUp(names []string) (map[string]map[string]string, map[string]string, error) {
	states, err := e.fetchStates(names)
	if err != nil {
		return nil, nil, err
	}
	pending := names // 第一轮无条件重查，见 upVerifyInterval
	for round := 0; round < upVerifyRounds && len(pending) > 0; round++ {
		sleepFunc(upVerifyInterval)
		fresh, ferr := e.fetchStates(pending)
		if ferr != nil {
			break // 查不到就沿用上一轮数据下结论，不再制造一条无关错误
		}
		var next []string
		for _, n := range pending {
			u := e.unitName(n)
			if f, ok := fresh[u]; ok {
				states[u] = f
			}
			if stillStarting(states[u]) {
				next = append(next, n)
			}
		}
		pending = next
	}
	notUp := make(map[string]string, len(names))
	for _, n := range names {
		if reason := notRunningReason(states[e.unitName(n)]); reason != "" {
			notUp[n] = reason
		}
	}
	return states, notUp, nil
}

// stillStarting 判断状态是否还在启动过程中、值得再等一轮。
// auto-restart 是"已经起来又立刻死了"，属定局结论不必再等；failed/inactive 同理。
// start_delay（ExecStartPre=/bin/sleep N）会让服务长时间停在 activating/start-pre，
// 等满轮数后由 notRunningReason 判为"仍在启动中"，不算失败。
func stillStarting(fields map[string]string) bool {
	if len(fields) == 0 || fields["ActiveState"] == "" {
		return true
	}
	return fields["ActiveState"] == "activating" && fields["SubState"] != "auto-restart"
}

// notRunningReason 返回"这个服务没正常运行"的人话原因，空串表示正常（active 下除 auto-restart
// 之外都算起来，含 exited/waiting/reloading，与 ps 的 STATUS 列同一口径）。
func notRunningReason(fields map[string]string) string {
	if len(fields) == 0 {
		return "未取到 systemd 状态"
	}
	switch {
	case fields["LoadState"] == "not-found":
		return "unit 未被 systemd 加载（文件没装上或 daemon-reload 未生效）"
	case fields["ActiveState"] == "failed":
		return "启动失败" + parenthetical(failureNote(fields))
	case fields["SubState"] == "auto-restart":
		return "主进程启动后立即退出，systemd 正在按 restart 反复拉起" +
			parenthetical(restartNote(fields), failureNote(fields))
	case fields["ActiveState"] == "inactive":
		return "主进程未运行（已退出）" + parenthetical(failureNote(fields))
	case fields["ActiveState"] == "activating":
		return "" // 仍在启动过程中（如 start_delay 计时中），不作失败结论
	}
	return ""
}

// systemdExecCodes 是 systemd 在 exec 前置阶段使用的专用退出码（systemd.exec(5) EXIT CODES 表）。
// 这些码不是业务进程返回的，而是 systemd 没能把进程跑起来，直接对应 yml 里的配置项。
var systemdExecCodes = map[string]string{
	"200": "退出码 200/CHDIR：working_dir 指定的工作目录不存在",
	"203": "退出码 203/EXEC：command 指定的可执行文件不存在或不可执行",
	"217": "退出码 217/USER：user 指定的用户不存在",
}

// systemdResults 是 systemd 的 Result 字段中常见失败取值的解释。
var systemdResults = map[string]string{
	"start-limit-hit": "systemd Result=start-limit-hit：短时间内重启次数超限，已放弃拉起",
	"timeout":         "systemd Result=timeout：启动/停止超时（可调 start_timeout / stop_timeout）",
	"exit-code":       "systemd Result=exit-code：进程以非 0 退出码结束",
	"signal":          "systemd Result=signal：进程被信号终止",
	"oom-kill":        "systemd Result=oom-kill：内存超限被内核杀掉（memory_max）",
}

// failureNote 取 systemd 自己对失败原因的说明（优先 exec 前置阶段的专用退出码，其次 Result），
// 返回不带括号的裸短语；取不到有效信息时返回空串，绝不编造。
func failureNote(fields map[string]string) string {
	if fields["ExecMainCode"] == "exited" {
		if m, ok := systemdExecCodes[fields["ExecMainStatus"]]; ok {
			return m
		}
	}
	if m, ok := systemdResults[fields["Result"]]; ok {
		return m
	}
	return ""
}

// restartNote 给出已自动重启的次数（0 或读不到时返回空串）。
func restartNote(fields map[string]string) string {
	n, err := strconv.ParseUint(fields["NRestarts"], 10, 64)
	if err != nil || n == 0 {
		return ""
	}
	return "已重启 " + strconv.FormatUint(n, 10) + " 次"
}

// parenthetical 把非空的补充说明合成一句 "（a，b）"，全为空时返回空串，避免出现空括号。
func parenthetical(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return "（" + strings.Join(kept, "，") + "）"
}
