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
	if strings.Contains(g.Content, "Requires=") {
		t.Errorf("默认软依赖不应写 Requires=\n%s", g.Content)
	}
	if g.Hash == "" {
		t.Error("hash is empty")
	}
}

func TestGenerateStrongDependency(t *testing.T) {
	// depends_on_required: true 时依赖写 Requires=（强依赖）而非 Wants=，顺序 After= 恒写。
	svc := &config.Service{
		Command:           "/opt/myapp/web",
		DependsOn:         []string{"api"},
		DependsOnRequired: true,
	}
	g, err := Generate("web", svc, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"After=myapp-api.service", "Requires=myapp-api.service"} {
		if !strings.Contains(g.Content, want) {
			t.Errorf("content missing %q\n%s", want, g.Content)
		}
	}
	if strings.Contains(g.Content, "Wants=") {
		t.Errorf("强依赖不应写 Wants=\n%s", g.Content)
	}
}

func TestGenerateStartDelay(t *testing.T) {
	// start_delay > 0 时生成 ExecStartPre=/bin/sleep N，且位于 ExecStart 之前。
	svc := &config.Service{Command: "/opt/bin/x", StartDelay: 5}
	g, err := Generate("x", svc, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "ExecStartPre=/bin/sleep 5") {
		t.Errorf("start_delay 应写入 ExecStartPre=/bin/sleep 5\n%s", g.Content)
	}
	if strings.Index(g.Content, "ExecStartPre=") > strings.Index(g.Content, "ExecStart=") {
		t.Errorf("ExecStartPre 应位于 ExecStart 之前\n%s", g.Content)
	}
}

func TestGenerateNoStartDelay(t *testing.T) {
	// start_delay 未配置（0）时不写 ExecStartPre。
	svc := &config.Service{Command: "/opt/bin/x"}
	g, err := Generate("x", svc, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g.Content, "ExecStartPre") {
		t.Errorf("未配置 start_delay 不应写 ExecStartPre\n%s", g.Content)
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
	// restart / restart_sec / start_timeout 未配置时写入默认 always / 5 / 90。
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
	if !strings.Contains(g.Content, "TimeoutStartSec=90") {
		t.Errorf("default start_timeout 应写入 TimeoutStartSec=90\n%s", g.Content)
	}
}

func TestGenerateStartTimeout(t *testing.T) {
	// start_timeout 显式配置时透传，同时 start_delay 场景可放宽启动超时。
	svc := &config.Service{Command: "/opt/bin/x", StartTimeout: 300}
	g, err := Generate("x", svc, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "TimeoutStartSec=300") {
		t.Errorf("start_timeout 应写入 TimeoutStartSec=300\n%s", g.Content)
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
