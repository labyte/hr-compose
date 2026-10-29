// Package unit 负责把服务配置渲染成 systemd unit 文件文本。
package unit

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"hr.compose/internal/config"
)

// ManagedMark 是 unit 文件的托管标记，down 删除前用于校验归属，防止误删同名系统服务。
const ManagedMark = "# MANAGED BY hr-compose"

// Generated 是单个服务生成的 unit 产物。
type Generated struct {
	UnitPath string // 如 api.service
	Content  string // 完整 unit 文本
	Hash     string // 内容 hash，用于 up 幂等比对
}

// Name 拼出 unit 文件名：<project>-<service>.service；project 为空时不加前缀
// （兼容直接构造 Config 的调用/测试）。project 来自 hr-compose.yml 的 name: 字段，
// 保证不同目录的同名服务落成不同 unit 文件，互不覆盖。
func Name(project, service string) string {
	if project == "" {
		return service + ".service"
	}
	return project + "-" + service + ".service"
}

// Generate 把服务配置渲染成 systemd unit 文本。project 是项目名（unit 文件名前缀），
// depends_on 引用的同 project 服务依赖名也带上前缀。
func Generate(name string, svc *config.Service, project string) (*Generated, error) {
	// 字段值里的换行会往 unit 里另起一行注入额外指令（如 Description 带换行可在 [Unit] 段塞入
	// ExecStartPre=），渲染前先拒绝。config.Load 已校验过，这里给绕过 Load 直接构造 Config 的调用方兜底。
	if err := config.ValidatePassthrough(name, svc); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(ManagedMark + " -- 由 hr-compose 生成，请勿手动编辑，改动会被覆盖\n")

	b.WriteString("\n[Unit]\n")
	fmt.Fprintf(&b, "Description=%s\n", svc.EffectiveDescription(name))
	if len(svc.DependsOn) > 0 {
		deps := make([]string, 0, len(svc.DependsOn))
		for _, d := range svc.DependsOn {
			deps = append(deps, Name(project, d))
		}
		joined := strings.Join(deps, " ")
		// 顺序恒写 After；强弱由依赖指令分流：默认软依赖 Wants=，
		// depends_on_required 时写 Requires= 实现强依赖（被依赖失败则本服务不启动、被停止则联动停止）。
		depDir := "Wants="
		if svc.DependsOnRequired {
			depDir = "Requires="
		}
		fmt.Fprintf(&b, "After=%s\n", joined)
		fmt.Fprintf(&b, "%s%s\n", depDir, joined)
	}

	b.WriteString("\n[Service]\n")
	// start_delay 延时启动：ExecStartPre sleep 在 After= 依赖启动完成后才执行，
	// 天然满足"依赖起来后再计时"；它计入启动超时 TimeoutStartSec，延时较大需同步配 start_timeout 放宽。
	if svc.StartDelay > 0 {
		fmt.Fprintf(&b, "ExecStartPre=/bin/sleep %d\n", svc.StartDelay)
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", svc.Command)
	if svc.WorkingDir != "" {
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", svc.WorkingDir)
	}
	if svc.Group != "" {
		fmt.Fprintf(&b, "Group=%s\n", svc.Group)
	}
	// user 未配置时自动注入执行 up 的真实用户（SUDO_USER / 当前用户）
	fmt.Fprintf(&b, "User=%s\n", svc.EffectiveUser())
	for _, env := range svc.Environment {
		fmt.Fprintf(&b, "Environment=%s\n", env)
	}
	if svc.StopSignal != "" {
		fmt.Fprintf(&b, "KillSignal=%s\n", svc.StopSignal)
	}
	if svc.StopTimeout > 0 {
		fmt.Fprintf(&b, "TimeoutStopSec=%d\n", svc.StopTimeout)
	}
	if svc.MemoryMax != "" {
		fmt.Fprintf(&b, "MemoryMax=%s\n", svc.MemoryMax)
	}
	if svc.CPUQuota != "" {
		fmt.Fprintf(&b, "CPUQuota=%s\n", svc.CPUQuota)
	}
	// restart / restart_sec / start_timeout / std_output 未配置时使用代码默认值（always / 5 / 90 / null）
	fmt.Fprintf(&b, "Restart=%s\n", svc.EffectiveRestart())
	fmt.Fprintf(&b, "RestartSec=%d\n", svc.EffectiveRestartSec())
	fmt.Fprintf(&b, "TimeoutStartSec=%d\n", svc.EffectiveStartTimeout())
	out := svc.EffectiveStdOutput()
	fmt.Fprintf(&b, "StandardOutput=%s\n", out)
	fmt.Fprintf(&b, "StandardError=%s\n", out)

	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")

	content := b.String()
	return &Generated{
		UnitPath: Name(project, name),
		Content:  content,
		Hash:     fmt.Sprintf("%x", sha256.Sum256([]byte(content))),
	}, nil
}
