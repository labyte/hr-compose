package engine

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"hr.compose/internal/config"
)

// capturePsOutput 把 ps 表的重渲染输出收到缓冲里，并在用例结束后恢复。
func capturePsOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	oldOut, oldColor := stdout, colorOverride
	buf := &bytes.Buffer{}
	stdout, colorOverride = buf, "never"
	t.Cleanup(func() { stdout, colorOverride = oldOut, oldColor })
	return buf
}

// dyingSys 在 Start/Restart 之后固定返回给定的"启动后状态"，用于模拟 systemd 的真实行为：
// start 返回 0 但进程压根没活着。
type dyingSys struct {
	stateSys
}

func newDyingSys(after map[string]map[string]string) *dyingSys {
	return &dyingSys{stateSys{after: after}}
}

func TestUpDetectsProcessThatDiesImmediately(t *testing.T) {
	// 真机实证（e2e/behavior-check.sh 的 D 项）：command 路径不存在时 `systemctl start` 仍返回 0
	// ——Type=simple 只要 fork 成功就算启动完成，Restart=always 随即把 unit 打进 auto-restart 循环。
	// 不复核状态就把服务报成"已启动"是最危险的谎报，必须报错并说清 systemd 给出的原因。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := newDyingSys(map[string]map[string]string{"api.service": {
		"ActiveState": "activating", "SubState": "auto-restart", "MainPID": "0",
		"NRestarts": "3", "ExecMainCode": "exited", "ExecMainStatus": "203",
		"MemoryCurrent": "[not set]",
	}})
	cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/no/such/binary"}}}
	out := capturePsOutput(t)
	err := New(cfg, fake).Up()
	if err == nil {
		t.Fatal("start 返回 0 但进程反复退出时 up 必须返回错误")
	}
	msg := err.Error()
	for _, want := range []string{"api", "203", "command", "重启 3 次"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息应含 %q，got:\n%s", want, msg)
		}
	}
	table := out.String()
	if !strings.Contains(table, "restarting") {
		t.Errorf("状态表应呈现 restarting\n%s", table)
	}
	// 未运行时内存列不该把 systemd 的内部字面量透给用户
	if strings.Contains(table, "[not set]") {
		t.Errorf("MEMORY 列不该透出 [not set]\n%s", table)
	}
}

func TestUpDetectsStartLimitHit(t *testing.T) {
	// 反复重启超限后 systemd 放弃拉起：ActiveState=failed + Result=start-limit-hit，
	// 这个 Result 取值是使用者最该看到的一句（说明要改 restart/restart_sec 或修进程本身）。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := newDyingSys(map[string]map[string]string{"api.service": {
		"ActiveState": "failed", "SubState": "failed", "Result": "start-limit-hit", "MainPID": "0",
	}})
	cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/x/api"}}}
	out := capturePsOutput(t)
	err := New(cfg, fake).Up()
	if err == nil {
		t.Fatal("服务处于 failed 时 up 必须返回错误")
	}
	if !strings.Contains(err.Error(), "start-limit-hit") {
		t.Errorf("应转述 systemd 的 Result，got:\n%v", err)
	}
	if !strings.Contains(out.String(), "failed") {
		t.Errorf("状态表应呈现 failed\n%s", out.String())
	}
}

func TestUpDoesNotFailServiceStillStarting(t *testing.T) {
	// start_delay 生成的是 ExecStartPre=/bin/sleep N，systemd 会长时间停在 activating/start-pre。
	// 这是按配置正常等待，复核轮数用满也不能判成失败（否则 start_delay 一大于轮询窗口就误报）。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := newDyingSys(map[string]map[string]string{"api.service": {
		"ActiveState": "activating", "SubState": "start-pre", "MainPID": "0",
	}})
	cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/x/api", StartDelay: 60}}}
	capturePsOutput(t)
	if err := New(cfg, fake).Up(); err != nil {
		t.Fatalf("仍在启动过程中不应报错，got: %v", err)
	}
}

func TestCheckUpWaitsUntilStateSettles(t *testing.T) {
	// 复核是轮询而非一次定论：第一轮还在 start-pre、第二轮才 running 的服务不该被报成失败。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := &settleSys{rounds: 2, starting: map[string]map[string]string{
		"api.service": {"ActiveState": "activating", "SubState": "start-pre", "MainPID": "0"},
	}}
	cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/x/api"}}}
	e := New(cfg, fake)
	states, notUp, err := e.checkUp([]string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	if len(notUp) > 0 {
		t.Errorf("第二轮已 running，不该有未起来的服务: %v", notUp)
	}
	if got := states["api.service"]["SubState"]; got != "running" {
		t.Errorf("checkUp 应返回最后一轮的状态，got %q", got)
	}
	if fake.calls < 3 { // 首轮快照 + 至少两轮复核
		t.Errorf("应轮询多次才下结论，查询次数=%d", fake.calls)
	}
}

// settleSys 前 rounds 次查询返回 starting 里的状态，之后返回正常 running。
type settleSys struct {
	fakeSys
	rounds   int
	calls    int
	starting map[string]map[string]string
}

func (s *settleSys) ShowMany(units []string) (map[string]map[string]string, error) {
	s.calls++
	m := make(map[string]map[string]string, len(units))
	for _, u := range units {
		if s.calls <= s.rounds {
			if custom, ok := s.starting[u]; ok {
				f := fakeShowFields()
				for k, v := range custom {
					f[k] = v
				}
				m[u] = f
				continue
			}
		}
		m[u] = fakeShowFields()
	}
	return m, nil
}

func TestNotRunningReason(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]string
		want   string // 期望的错误片段；空串表示"不算没起来"
	}{
		{"运行中", map[string]string{"ActiveState": "active", "SubState": "running", "LoadState": "loaded"}, ""},
		{"oneshot 跑完也算起来", map[string]string{"ActiveState": "active", "SubState": "exited", "LoadState": "loaded"}, ""},
		{"notify 等待就绪也算起来", map[string]string{"ActiveState": "active", "SubState": "waiting", "LoadState": "loaded"}, ""},
		{"启动中不算失败", map[string]string{"ActiveState": "activating", "SubState": "start", "LoadState": "loaded"}, ""},
		{"未安装", map[string]string{"ActiveState": "inactive", "SubState": "dead", "LoadState": "not-found"}, "未被 systemd 加载"},
		{"失败", map[string]string{"ActiveState": "failed", "SubState": "failed", "LoadState": "loaded"}, "启动失败"},
		{"反复重启", map[string]string{"ActiveState": "activating", "SubState": "auto-restart", "LoadState": "loaded"}, "反复拉起"},
		{"已退出", map[string]string{"ActiveState": "inactive", "SubState": "dead", "LoadState": "loaded"}, "未运行"},
		{"停止中不算没起来", map[string]string{"ActiveState": "deactivating", "SubState": "stop", "LoadState": "loaded"}, ""},
		{"完全没数据", map[string]string{}, "未取到 systemd 状态"},
	}
	for _, tc := range cases {
		got := notRunningReason(tc.fields)
		if tc.want == "" {
			if got != "" {
				t.Errorf("%s: 不该报失败，got %q", tc.name, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: reason = %q，期望含 %q", tc.name, got, tc.want)
		}
	}
}

func TestFailureNoteUsesSystemdOwnAccounting(t *testing.T) {
	// 203/200/217 是 systemd 在 exec 前置阶段用的专用退出码，直接对应 yml 字段，必须翻成修复指引
	for status, want := range map[string]string{
		"203": "command",
		"200": "working_dir",
		"217": "user",
	} {
		if got := failureNote(map[string]string{"ExecMainCode": "exited", "ExecMainStatus": status}); !strings.Contains(got, want) {
			t.Errorf("退出码 %s 应指向 %q，got %q", status, want, got)
		}
	}
	// 业务进程自己的退出码不属 systemd 专用码，不擅自解释
	if got := failureNote(map[string]string{"ExecMainCode": "exited", "ExecMainStatus": "1"}); got != "" {
		t.Errorf("不该解释业务退出码，got %q", got)
	}
	// ExecMainCode 不是 exited（如被信号杀）时不套用退出码表
	if got := failureNote(map[string]string{"ExecMainCode": "killed", "ExecMainStatus": "203"}); got != "" {
		t.Errorf("code=killed 时不该按退出码表解释，got %q", got)
	}
	// Result 是 systemd 的失败口径，oom-kill 要指回 memory_max
	if got := failureNote(map[string]string{"Result": "oom-kill"}); !strings.Contains(got, "memory_max") {
		t.Errorf("oom-kill 应提到 memory_max，got %q", got)
	}
	// 成功或没有信息时不编造
	for _, f := range []map[string]string{{"Result": "success"}, {}, nil} {
		if got := failureNote(f); got != "" {
			t.Errorf("无失败信息时不该有补充，got %q", got)
		}
	}
}

func TestRestartNote(t *testing.T) {
	if got := restartNote(map[string]string{"NRestarts": "7"}); !strings.Contains(got, "7") {
		t.Errorf("应给出重启次数，got %q", got)
	}
	for _, v := range []string{"0", "", "abc"} {
		if got := restartNote(map[string]string{"NRestarts": v}); got != "" {
			t.Errorf("NRestarts=%q 时不该显示次数，got %q", v, got)
		}
	}
}

func TestParenthetical(t *testing.T) {
	// 多条补充合成一对括号，全空时不留下空括号（否则输出里出现"启动失败（）"这种噪声）
	if got := parenthetical("已重启 3 次", "退出码 203/EXEC"); got != "（已重启 3 次，退出码 203/EXEC）" {
		t.Errorf("got %q", got)
	}
	if got := parenthetical("", "只有这一条"); got != "（只有这一条）" {
		t.Errorf("got %q", got)
	}
	if got := parenthetical("", ""); got != "" {
		t.Errorf("无补充时应为空串，got %q", got)
	}
	if got := notRunningReason(map[string]string{"ActiveState": "failed", "SubState": "failed", "LoadState": "loaded"}); got != "启动失败" {
		t.Errorf("无 systemd 细节时不该有空括号，got %q", got)
	}
}

// 复核的等待与轮数是有意的取舍：happy path 多一次查询（≈300ms），换来"绝不谎报成功"。
func TestUpVerifyWindowIsBounded(t *testing.T) {
	if upVerifyInterval <= 0 || upVerifyRounds <= 0 {
		t.Fatal("复核窗口必须为正")
	}
	if total := upVerifyInterval * time.Duration(upVerifyRounds); total > 3*time.Second {
		t.Errorf("复核最长等待 %v 过久，up 不该挂住用户这么久", total)
	}
}
