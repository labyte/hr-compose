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
	if !strings.Contains(out, "api.service") || strings.Contains(out, "redis.service") {
		t.Errorf("Config(api) 应只包含 api.service\n%s", out)
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
	if !strings.Contains(out, "api.service") || !strings.Contains(out, "redis.service") {
		t.Errorf("Config() 应包含全部服务\n%s", out)
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
