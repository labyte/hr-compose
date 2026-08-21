package engine

import (
	"bytes"
	"strings"
	"testing"

	"hr.compose/internal/config"
)

func TestConfigFilter(t *testing.T) {
	oldOut := stdout
	stdout = &bytes.Buffer{}
	defer func() { stdout = oldOut }()

	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", Description: "API 服务"},
		"redis": {Command: "/x/redis"},
	}}
	if err := New(cfg, &fakeSys{}).Config("api"); err != nil {
		t.Fatal(err)
	}
	out := stdout.(*bytes.Buffer).String()
	// 段头应标注完整 unit 文件路径（UnitDir 默认 /etc/systemd/system）
	if !strings.Contains(out, "/etc/systemd/system/api.service") || strings.Contains(out, "redis.service") {
		t.Errorf("Config(api) 应只包含 api 的完整路径\n%s", out)
	}
	if !strings.Contains(out, "Description=API 服务") {
		t.Errorf("应包含 api 的描述\n%s", out)
	}
}

func TestConfigAll(t *testing.T) {
	oldOut := stdout
	stdout = &bytes.Buffer{}
	defer func() { stdout = oldOut }()

	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api"},
		"redis": {Command: "/x/redis"},
	}}
	if err := New(cfg, &fakeSys{}).Config(""); err != nil {
		t.Fatal(err)
	}
	out := stdout.(*bytes.Buffer).String()
	for _, want := range []string{"/etc/systemd/system/api.service", "/etc/systemd/system/redis.service"} {
		if !strings.Contains(out, want) {
			t.Errorf("Config() 应包含完整路径 %q\n%s", want, out)
		}
	}
}

func TestConfigUnknownService(t *testing.T) {
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	if err := New(cfg, &fakeSys{}).Config("nope"); err == nil {
		t.Error("未知服务名应报错")
	}
}
