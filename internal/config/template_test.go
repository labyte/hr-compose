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
		"version:", "name:", "services:", "api:", "web:", "command", "depends_on:", "示例",
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

func TestInitFromDirScansServices(t *testing.T) {
	services := filepath.Join(t.TempDir(), "services")
	// api：放一个同名可执行文件，应被探测到
	apiDir := filepath.Join(services, "api")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	apiBin := filepath.Join(apiDir, "api")
	if err := os.WriteFile(apiBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// web：空目录，应回退占位 ./web
	webDir := filepath.Join(services, "web")
	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 隐藏目录与普通文件都不应成为服务
	if err := os.MkdirAll(filepath.Join(services, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(services, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "hr-compose.yml")
	if err := InitFromDir(services, out); err != nil {
		t.Fatalf("InitFromDir: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	for _, want := range []string{
		"version:", "name:", "services:",
		"api:", "web:",
		"command: " + apiBin, // 探测到同名可执行文件
		"working_dir: " + apiDir,
		"command: ./web", // 空目录回退占位
		"TODO",           // 占位带提示
		"字段说明",           // 保留字段说明注释
	} {
		if !strings.Contains(content, want) {
			t.Errorf("扫描生成应含 %q\n%s", want, content)
		}
	}
	if strings.Contains(content, ".git") || strings.Contains(content, "readme.txt") {
		t.Errorf("隐藏目录/普通文件不应成为服务\n%s", content)
	}
	// 生成的 yml 必须能通过 Load 校验
	cfg, err := Load(out)
	if err != nil {
		t.Fatalf("扫描生成应能通过 Load 校验: %v", err)
	}
	if len(cfg.Services) != 2 {
		t.Errorf("应有 2 个服务，实际 %d", len(cfg.Services))
	}
	if _, ok := cfg.Services["api"]; !ok {
		t.Error("缺少 api 服务")
	}
	if got := cfg.Services["web"].Command; got != "./web" {
		t.Errorf("web 的 command 应为占位 ./web，实际 %q", got)
	}
}

func TestInitFromDirRefusesOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hr-compose.yml")
	if err := os.WriteFile(p, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InitFromDir(t.TempDir(), p); err == nil {
		t.Fatal("文件已存在时应拒绝覆盖")
	}
}

func TestInitFromDirEmptyDir(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InitFromDir(empty, filepath.Join(t.TempDir(), "hr-compose.yml")); err == nil {
		t.Fatal("无服务子目录时应报错，避免生成必然不合法的空 services")
	}
}

func TestInitFromDirNameCollision(t *testing.T) {
	services := filepath.Join(t.TempDir(), "services")
	// "my-app" 与 "My App" sanitize 后同名 my-app
	for _, name := range []string{"my-app", "My App"} {
		if err := os.MkdirAll(filepath.Join(services, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := InitFromDir(services, filepath.Join(t.TempDir(), "hr-compose.yml")); err == nil {
		t.Fatal("sanitize 后同名时应报错，不能静默覆盖其中一个")
	}
}
