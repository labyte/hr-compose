package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hr-compose.yml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	p := writeFixture(t, `
services:
  api:
    command: /opt/myapp/api
    depends_on: [redis]
  redis:
    command: /opt/redis/redis-server
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Services) != 2 {
		t.Fatalf("want 2 services, got %d", len(cfg.Services))
	}
	if got := cfg.Services["api"].EffectiveStdOutput(); got != "null" {
		t.Errorf("default std_output = %q, want null", got)
	}
}

func TestLoadRecordsServiceOrder(t *testing.T) {
	// 声明顺序与字母序相反（web > api > zzz），ServiceOrder 应按声明顺序记录
	p := writeFixture(t, `
services:
  web:
    command: /opt/myapp/web
    depends_on: [api]
  api:
    command: /opt/myapp/api
  zzz:
    command: /opt/myapp/zzz
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"web", "api", "zzz"}
	if len(cfg.ServiceOrder) != len(want) {
		t.Fatalf("ServiceOrder = %v, want %v", cfg.ServiceOrder, want)
	}
	for i := range want {
		if cfg.ServiceOrder[i] != want[i] {
			t.Errorf("ServiceOrder[%d] = %q, want %q", i, cfg.ServiceOrder[i], want[i])
		}
	}
}

func TestRejectUnknownField(t *testing.T) {
	p := writeFixture(t, `
services:
  api:
    command: /opt/myapp/api
    foobar: 1
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for unknown field")
	}
}

func TestRejectMissingCommand(t *testing.T) {
	p := writeFixture(t, `
services:
  api:
    working_dir: /opt/myapp
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for missing command")
	}
}

func TestRejectBadDependsOn(t *testing.T) {
	p := writeFixture(t, `
services:
  api:
    command: /opt/myapp/api
    depends_on: [not_exists]
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for undefined depends_on")
	}
}

func TestRejectInvalidServiceName(t *testing.T) {
	p := writeFixture(t, `
services:
  "Api.Svc":
    command: /opt/myapp/api
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for invalid service name")
	}
}

func TestRejectEmptyServices(t *testing.T) {
	p := writeFixture(t, `
services: {}
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for empty services")
	}
}

func TestQuotedNullStdOutput(t *testing.T) {
	p := writeFixture(t, `
services:
  worker:
    command: /opt/worker
    std_output: "null"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveStdOutput(); got != "null" {
		t.Errorf("EffectiveStdOutput = %q, want \"null\"", got)
	}
}

func TestUnquotedNullDefaultsToNull(t *testing.T) {
	// 未加引号的 null 是 YAML null 字面量，等价于"未配置"，按默认 null（丢弃输出）处理。
	p := writeFixture(t, `
services:
  worker:
    command: /opt/worker
    std_output: null
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveStdOutput(); got != "null" {
		t.Errorf("EffectiveStdOutput = %q, want \"null\"", got)
	}
}

func TestNoneStdOutputAlias(t *testing.T) {
	// std_output: none 是旧版本推荐的写法，兼容保留，同样归一为 "null"。
	p := writeFixture(t, `
services:
  worker:
    command: /opt/worker
    std_output: none
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveStdOutput(); got != "null" {
		t.Errorf("EffectiveStdOutput = %q, want \"null\"", got)
	}
}

func TestDefaultRestart(t *testing.T) {
	// restart / restart_sec 未配置时走代码默认：always / 5。
	p := writeFixture(t, `
services:
  worker:
    command: /opt/worker
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	svc := cfg.Services["worker"]
	if got := svc.EffectiveRestart(); got != "always" {
		t.Errorf("EffectiveRestart = %q, want always", got)
	}
	if got := svc.EffectiveRestartSec(); got != 5 {
		t.Errorf("EffectiveRestartSec = %d, want 5", got)
	}
}

func TestExplicitRestartPassthrough(t *testing.T) {
	p := writeFixture(t, `
services:
  worker:
    command: /opt/worker
    restart: on-failure
    restart_sec: 10
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	svc := cfg.Services["worker"]
	if got := svc.EffectiveRestart(); got != "on-failure" {
		t.Errorf("EffectiveRestart = %q, want on-failure", got)
	}
	if got := svc.EffectiveRestartSec(); got != 10 {
		t.Errorf("EffectiveRestartSec = %d, want 10", got)
	}
}

func TestEffectiveUserPassthrough(t *testing.T) {
	p := writeFixture(t, `
services:
  worker:
    command: /opt/worker
    user: appuser
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveUser(); got != "appuser" {
		t.Errorf("EffectiveUser = %q, want appuser", got)
	}
}

func TestEffectiveUserDefaultsToResolver(t *testing.T) {
	// user 未配置时自动注入 userResolver 的结果（执行 up 的真实用户）。
	old := userResolver
	defer func() { userResolver = old }()
	userResolver = func() string { return "alice" }
	p := writeFixture(t, `
services:
  worker:
    command: /opt/worker
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveUser(); got != "alice" {
		t.Errorf("EffectiveUser = %q, want alice", got)
	}
}

func TestUserResolverPrefersSudoUser(t *testing.T) {
	// sudo 下进程是 root，真实用户在 SUDO_USER 环境变量里。
	old, had := os.LookupEnv("SUDO_USER")
	t.Cleanup(func() {
		if had {
			os.Setenv("SUDO_USER", old)
		} else {
			os.Unsetenv("SUDO_USER")
		}
	})
	os.Setenv("SUDO_USER", "bob")
	if got := userResolver(); got != "bob" {
		t.Errorf("userResolver = %q, want bob", got)
	}
}

func TestParseDescription(t *testing.T) {
	p := writeFixture(t, `
services:
  api:
    description: 主业务 API 服务
    command: /opt/myapp/api
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	svc := cfg.Services["api"]
	if svc.Description != "主业务 API 服务" {
		t.Errorf("Description = %q, want 主业务 API 服务", svc.Description)
	}
	if got := svc.EffectiveDescription("api"); got != "主业务 API 服务" {
		t.Errorf("EffectiveDescription = %q, want 配置的描述", got)
	}
}

func TestEffectiveDescriptionDefault(t *testing.T) {
	p := writeFixture(t, `
services:
  api:
    command: /opt/myapp/api
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["api"].EffectiveDescription("api"); got != "hr-compose service api" {
		t.Errorf("EffectiveDescription = %q, want 默认值", got)
	}
}

func TestRejectNewlineInCommand(t *testing.T) {
	p := writeFixture(t, `
services:
  api:
    command: "/bin/true\nExecStart=/bin/evil"
`)
	if _, err := Load(p); err == nil {
		t.Fatal("command 含换行应被拒绝（unit 注入）")
	}
}

func TestRejectNewlineInEnvironment(t *testing.T) {
	p := writeFixture(t, `
services:
  api:
    command: /opt/myapp/api
    environment:
      - "FOO=1\nExecStart=/bin/evil"
`)
	if _, err := Load(p); err == nil {
		t.Fatal("environment 含换行应被拒绝（unit 注入）")
	}
}
