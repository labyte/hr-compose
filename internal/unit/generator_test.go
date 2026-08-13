package unit

import (
	"strings"
	"testing"

	"github.com/labyte/hr-compose/internal/config"
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
	g, err := Generate("api", svc)
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
	svc := &config.Service{Command: "/opt/bin/x"}
	g, err := Generate("x", svc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "StandardOutput=journal") {
		t.Error("default std_output should be journal")
	}
}

func TestGenerateNoDependsOn(t *testing.T) {
	svc := &config.Service{Command: "/opt/bin/x"}
	g, err := Generate("x", svc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g.Content, "After=") || strings.Contains(g.Content, "Wants=") {
		t.Error("should not emit After/Wants without depends_on")
	}
}
