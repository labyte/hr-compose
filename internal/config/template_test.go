package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWritesTemplate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hr-compose.yml")
	if err := Init(p); err != nil {
		t.Fatalf("Init: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	for _, want := range []string{
		"version:", "services:", "api:", "web:", "command", "depends_on:", "示例",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("模板缺少 %q\n%s", want, content)
		}
	}
	// 生成的模板必须能通过 Load 校验：api / web 两个服务，web 依赖 api
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("生成的模板应能通过 Load 校验: %v", err)
	}
	if len(cfg.Services) != 2 {
		t.Errorf("模板应有 2 个服务，实际 %d", len(cfg.Services))
	}
	if _, ok := cfg.Services["api"]; !ok {
		t.Error("模板缺少 api 服务")
	}
	web := cfg.Services["web"]
	if web == nil || len(web.DependsOn) != 1 || web.DependsOn[0] != "api" {
		t.Errorf("web 服务应依赖 api，实际 DependsOn=%v", web.DependsOn)
	}
}

func TestInitRefusesOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hr-compose.yml")
	if err := os.WriteFile(p, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Init(p); err == nil {
		t.Fatal("文件已存在时应拒绝覆盖")
	}
	if b, _ := os.ReadFile(p); string(b) != "existing" {
		t.Error("已存在的文件不应被改写")
	}
}
