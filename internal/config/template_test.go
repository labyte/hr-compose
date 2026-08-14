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
	for _, want := range []string{"version:", "services:", "command"} {
		if !strings.Contains(content, want) {
			t.Errorf("模板缺少 %q\n%s", want, content)
		}
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
