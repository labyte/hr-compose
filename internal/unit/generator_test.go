package unit

import (
	"strings"
	"testing"

	"hr.compose/internal/config"
)

func TestGenerate(t *testing.T) {
	svc := &config.Service{
		Command:    "/opt/myapp/api",
		WorkingDir: "/opt/myapp",
		User:       "appuser",
		Restart:    "on-failure",
		RestartSec: 5,
		StdOutput:  "append:/data/logs/api.log",
		DependsOn:  []string{"redis"},
	}
	g, err := Generate("api", svc)
	if err != nil {
		t.Fatal(err)
	}
	if g.UnitPath != "api.service" {
		t.Errorf("UnitPath = %q, want api.service", g.UnitPath)
	}
	for _, want := range []string{
		"# MANAGED BY hr-compose",
		"ExecStart=/opt/myapp/api",
		"WorkingDirectory=/opt/myapp",
		"User=appuser",
		"Restart=on-failure",
		"RestartSec=5",
		"After=redis.service",
		"Wants=redis.service",
		"StandardOutput=append:/data/logs/api.log",
		"StandardError=append:/data/logs/api.log",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(g.Content, want) {
			t.Errorf("content missing %q\n%s", want, g.Content)
		}
	}
	if g.Hash == "" {
		t.Error("hash is empty")
	}
}

func TestGenerateDefaultStdOutput(t *testing.T) {
	svc := &config.Service{Command: "/opt/bin/x"}
	g, err := Generate("x", svc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "StandardOutput=journal") {
		t.Error("default std_output should be journal")
	}
}

func TestGenerateNoDependsOn(t *testing.T) {
	svc := &config.Service{Command: "/opt/bin/x"}
	g, err := Generate("x", svc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g.Content, "After=") || strings.Contains(g.Content, "Wants=") {
		t.Error("should not emit After/Wants without depends_on")
	}
}

func TestGenerateDescription(t *testing.T) {
	svc := &config.Service{Command: "/opt/bin/x", Description: "主业务 API 服务"}
	g, err := Generate("x", svc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "Description=主业务 API 服务") {
		t.Errorf("自定义 description 应写入 Description=\n%s", g.Content)
	}
}

func TestGenerateDefaultDescription(t *testing.T) {
	svc := &config.Service{Command: "/opt/bin/x"}
	g, err := Generate("x", svc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "Description=hr-compose service x") {
		t.Errorf("未配置 description 时应使用默认值\n%s", g.Content)
	}
}
