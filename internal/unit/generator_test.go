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
	g, err := Generate("api", svc, "")
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
	// std_output 未配置时按默认 null（丢弃输出）写入。
	svc := &config.Service{Command: "/opt/bin/x"}
	g, err := Generate("x", svc, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "StandardOutput=null") {
		t.Errorf("default std_output 应写入 StandardOutput=null\n%s", g.Content)
	}
	if !strings.Contains(g.Content, "StandardError=null") {
		t.Errorf("default std_output 应写入 StandardError=null\n%s", g.Content)
	}
}

func TestGenerateDefaultRestart(t *testing.T) {
	// restart / restart_sec 未配置时写入默认 always / 5。
	svc := &config.Service{Command: "/opt/bin/x"}
	g, err := Generate("x", svc, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "Restart=always") {
		t.Errorf("default restart 应写入 Restart=always\n%s", g.Content)
	}
	if !strings.Contains(g.Content, "RestartSec=5") {
		t.Errorf("default restart_sec 应写入 RestartSec=5\n%s", g.Content)
	}
}

func TestGenerateDefaultUser(t *testing.T) {
	// user 未配置时注入 User=（执行 up 的真实用户），不产生空 User=。
	svc := &config.Service{Command: "/opt/bin/x"}
	g, err := Generate("x", svc, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "User=") {
		t.Errorf("default user 应写入 User=\n%s", g.Content)
	}
}

func TestGenerateNoneStdOutput(t *testing.T) {
	// std_output: none 别名应归一为 null，写入 StandardOutput/StandardError=null。
	svc := &config.Service{Command: "/opt/bin/x", StdOutput: "none"}
	g, err := Generate("x", svc, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "StandardOutput=null") {
		t.Errorf("std_output=none 应写入 StandardOutput=null\n%s", g.Content)
	}
	if !strings.Contains(g.Content, "StandardError=null") {
		t.Errorf("std_output=none 应写入 StandardError=null\n%s", g.Content)
	}
	if strings.Contains(g.Content, "StandardOutput=none") {
		t.Errorf("unit 中不应出现原始值 none\n%s", g.Content)
	}
}

func TestGenerateNoDependsOn(t *testing.T) {
	svc := &config.Service{Command: "/opt/bin/x"}
	g, err := Generate("x", svc, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g.Content, "After=") || strings.Contains(g.Content, "Wants=") {
		t.Error("should not emit After/Wants without depends_on")
	}
}

func TestGenerateDescription(t *testing.T) {
	svc := &config.Service{Command: "/opt/bin/x", Description: "主业务 API 服务"}
	g, err := Generate("x", svc, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "Description=主业务 API 服务") {
		t.Errorf("自定义 description 应写入 Description=\n%s", g.Content)
	}
}

func TestGenerateDefaultDescription(t *testing.T) {
	svc := &config.Service{Command: "/opt/bin/x"}
	g, err := Generate("x", svc, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "Description=hr-compose service x") {
		t.Errorf("未配置 description 时应使用默认值\n%s", g.Content)
	}
}

func TestName(t *testing.T) {
	if got := Name("", "api"); got != "api.service" {
		t.Errorf(`Name("", "api") = %q, want api.service`, got)
	}
	if got := Name("myapp", "api"); got != "myapp-api.service" {
		t.Errorf(`Name("myapp", "api") = %q, want myapp-api.service`, got)
	}
}

func TestGenerateProjectPrefix(t *testing.T) {
	// project 非空时 UnitPath 带前缀，depends_on 依赖名同样带前缀（同 project 内引用）。
	svc := &config.Service{Command: "/opt/myapp/api", DependsOn: []string{"redis"}}
	g, err := Generate("api", svc, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if g.UnitPath != "myapp-api.service" {
		t.Errorf("UnitPath = %q, want myapp-api.service", g.UnitPath)
	}
	for _, want := range []string{"After=myapp-redis.service", "Wants=myapp-redis.service"} {
		if !strings.Contains(g.Content, want) {
			t.Errorf("content missing %q\n%s", want, g.Content)
		}
	}
}
