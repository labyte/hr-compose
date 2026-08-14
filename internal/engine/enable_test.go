package engine

import (
	"strings"
	"testing"

	"hr.compose/internal/config"
)

func TestEnableAll(t *testing.T) {
	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api"},
		"redis": {Command: "/x/redis"},
	}}
	if err := New(cfg, fake).Enable(""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"enable api.service", "enable redis.service"} {
		if !containsAction(fake.actions, want) {
			t.Errorf("actions 缺少 %q: %v", want, fake.actions)
		}
	}
}

func TestDisableSingle(t *testing.T) {
	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api"},
		"redis": {Command: "/x/redis"},
	}}
	if err := New(cfg, fake).Disable("api"); err != nil {
		t.Fatal(err)
	}
	if len(fake.actions) != 1 || fake.actions[0] != "disable api.service" {
		t.Errorf("Disable(api) = %v, want 仅 disable api.service", fake.actions)
	}
}

func containsAction(actions []string, want string) bool {
	for _, a := range actions {
		if strings.Contains(a, want) {
			return true
		}
	}
	return false
}
