package engine

import (
	"bytes"
	"os"
	"path/filepath"
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
	if err := New(cfg, &fakeSys{}).Config([]string{"api"}, false); err != nil {
		t.Fatal(err)
	}
	out := stdout.(*bytes.Buffer).String()
	// 预览模式段头应标注"预览"与将写入的完整 unit 文件路径。
	// 路径由 filepath.Join(UnitDir, ...) 生成，断言同样按当前平台拼接（Linux 下即 /etc/systemd/system/api.service）。
	wantPath := filepath.Join(UnitDir, "api.service")
	if !strings.Contains(out, "预览") || !strings.Contains(out, wantPath) {
		t.Errorf("Config(api) 应标注预览与 %s\n%s", wantPath, out)
	}
	if strings.Contains(out, "redis.service") {
		t.Errorf("Config(api) 不应包含 redis\n%s", out)
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
	if err := New(cfg, &fakeSys{}).Config(nil, false); err != nil {
		t.Fatal(err)
	}
	out := stdout.(*bytes.Buffer).String()
	for _, name := range []string{"api", "redis"} {
		// 路径按当前平台拼接断言（Linux 下即 /etc/systemd/system/<name>.service）
		wantPath := filepath.Join(UnitDir, name+".service")
		if !strings.Contains(out, wantPath) {
			t.Errorf("Config() 应包含完整路径 %q\n%s", wantPath, out)
		}
	}
}

func TestConfigRealShowsOnDiskFile(t *testing.T) {
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	oldOut := stdout
	stdout = &bytes.Buffer{}
	defer func() { stdout = oldOut }()

	// 磁盘上已存在一份与预览不同的内容（模拟手动改过的外部 unit）
	disk := "# 手动维护的 unit\n[Service]\nExecStart=/manual\n"
	if err := os.WriteFile(filepath.Join(UnitDir, "api.service"), []byte(disk), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	if err := New(cfg, &fakeSys{}).Config([]string{"api"}, true); err != nil {
		t.Fatal(err)
	}
	out := stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "实际文件") {
		t.Errorf("实际文件模式段头应标注实际文件\n%s", out)
	}
	if !strings.Contains(out, disk) {
		t.Errorf("应展示磁盘上的真实文件内容\n%s", out)
	}
	if strings.Contains(out, "预览") {
		t.Errorf("实际文件模式不应出现预览标注\n%s", out)
	}
}

func TestConfigRealFileMissing(t *testing.T) {
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	oldOut := stdout
	stdout = &bytes.Buffer{}
	defer func() { stdout = oldOut }()

	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	if err := New(cfg, &fakeSys{}).Config([]string{"api"}, true); err != nil {
		t.Fatal(err)
	}
	out := stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "文件不存在") {
		t.Errorf("文件不存在时应给出提示\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(UnitDir, "api.service")) {
		t.Errorf("提示中应含被检查的文件路径\n%s", out)
	}
}

func TestConfigUnknownService(t *testing.T) {
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	if err := New(cfg, &fakeSys{}).Config([]string{"nope"}, false); err == nil {
		t.Error("未知服务名应报错")
	}
}
