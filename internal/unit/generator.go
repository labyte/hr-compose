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

// Generate 把服务配置渲染成 systemd unit 文本。
func Generate(name string, svc *config.Service) (*Generated, error) {
	var b strings.Builder
	b.WriteString(ManagedMark + " -- 由 hr-compose 生成，请勿手动编辑，改动会被覆盖\n")

	b.WriteString("\n[Unit]\n")
	fmt.Fprintf(&b, "Description=%s\n", svc.EffectiveDescription(name))
	if len(svc.DependsOn) > 0 {
		deps := make([]string, 0, len(svc.DependsOn))
		for _, d := range svc.DependsOn {
			deps = append(deps, d+".service")
		}
		joined := strings.Join(deps, " ")
		fmt.Fprintf(&b, "After=%s\n", joined)
		fmt.Fprintf(&b, "Wants=%s\n", joined)
	}

	b.WriteString("\n[Service]\n")
	fmt.Fprintf(&b, "ExecStart=%s\n", svc.Command)
	if svc.WorkingDir != "" {
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", svc.WorkingDir)
	}
	if svc.User != "" {
		fmt.Fprintf(&b, "User=%s\n", svc.User)
	}
	if svc.Group != "" {
		fmt.Fprintf(&b, "Group=%s\n", svc.Group)
	}
	for _, env := range svc.Environment {
		fmt.Fprintf(&b, "Environment=%s\n", env)
	}
	if svc.Restart != "" {
		fmt.Fprintf(&b, "Restart=%s\n", svc.Restart)
	}
	if svc.RestartSec > 0 {
		fmt.Fprintf(&b, "RestartSec=%d\n", svc.RestartSec)
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
	out := svc.EffectiveStdOutput()
	fmt.Fprintf(&b, "StandardOutput=%s\n", out)
	fmt.Fprintf(&b, "StandardError=%s\n", out)

	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")

	content := b.String()
	return &Generated{
		UnitPath: name + ".service",
		Content:  content,
		Hash:     fmt.Sprintf("%x", sha256.Sum256([]byte(content))),
	}, nil
}
