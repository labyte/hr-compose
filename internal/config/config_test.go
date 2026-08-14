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
	if got := cfg.Services["api"].EffectiveStdOutput(); got != "journal" {
		t.Errorf("default std_output = %q, want journal", got)
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

func TestUnquotedNullDefaultsToJournal(t *testing.T) {
	// 未加引号的 null 是 YAML null 字面量，等价于"未配置"，按默认 journal 处理。
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
	if got := cfg.Services["worker"].EffectiveStdOutput(); got != "journal" {
		t.Errorf("EffectiveStdOutput = %q, want journal", got)
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
