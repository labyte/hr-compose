package engine

import (
	"testing"

	"hr.compose/internal/config"
)

func TestStartFollowsDependencyOrder(t *testing.T) {
	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", DependsOn: []string{"redis"}},
		"redis": {Command: "/x/redis"},
	}}
	if err := New(cfg, fake).Start(); err != nil {
		t.Fatal(err)
	}
	idx := func(s string) int {
		for i, a := range fake.actions {
			if a == s {
				return i
			}
		}
		t.Fatalf("action %q not executed, got %v", s, fake.actions)
		return -1
	}
	if i, j := idx("start redis.service"), idx("start api.service"); i > j {
		t.Errorf("redis 应在 api 之前启动: %v", fake.actions)
	}
}

func TestStopReverseOrder(t *testing.T) {
	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", DependsOn: []string{"redis"}},
		"redis": {Command: "/x/redis"},
	}}
	if err := New(cfg, fake).Stop(); err != nil {
		t.Fatal(err)
	}
	idx := func(s string) int {
		for i, a := range fake.actions {
			if a == s {
				return i
			}
		}
		t.Fatalf("action %q not executed, got %v", s, fake.actions)
		return -1
	}
	if i, j := idx("stop api.service"), idx("stop redis.service"); i > j {
		t.Errorf("api（依赖者）应先于 redis 停止: %v", fake.actions)
	}
}

func TestStartStopSingleName(t *testing.T) {
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", DependsOn: []string{"redis"}},
		"redis": {Command: "/x/redis"},
	}}
	fake := &fakeSys{}
	if err := New(cfg, fake).Start("api"); err != nil {
		t.Fatal(err)
	}
	if len(fake.actions) != 1 || fake.actions[0] != "start api.service" {
		t.Errorf("Start(api) = %v, want 仅 start api.service", fake.actions)
	}
	fake = &fakeSys{}
	if err := New(cfg, fake).Stop("api"); err != nil {
		t.Fatal(err)
	}
	if len(fake.actions) != 1 || fake.actions[0] != "stop api.service" {
		t.Errorf("Stop(api) = %v, want 仅 stop api.service", fake.actions)
	}
}

func TestStartMultipleNames(t *testing.T) {
	// 无依赖服务按输入顺序启动
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api"},
		"redis": {Command: "/x/redis"},
	}}
	fake := &fakeSys{}
	if err := New(cfg, fake).Start("redis", "api"); err != nil {
		t.Fatal(err)
	}
	want := []string{"start redis.service", "start api.service"}
	if len(fake.actions) != len(want) {
		t.Fatalf("Start(redis, api) actions = %v, want %v", fake.actions, want)
	}
	for i := range want {
		if fake.actions[i] != want[i] {
			t.Errorf("actions[%d] = %q, want %q", i, fake.actions[i], want[i])
		}
	}
}

func TestStopMultipleReverseDependency(t *testing.T) {
	// api 依赖 redis：stop 时依赖者 api 先停，再停 redis
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", DependsOn: []string{"redis"}},
		"redis": {Command: "/x/redis"},
	}}
	fake := &fakeSys{}
	if err := New(cfg, fake).Stop("api", "redis"); err != nil {
		t.Fatal(err)
	}
	want := []string{"stop api.service", "stop redis.service"}
	if len(fake.actions) != len(want) {
		t.Fatalf("Stop(api, redis) actions = %v, want %v", fake.actions, want)
	}
	for i := range want {
		if fake.actions[i] != want[i] {
			t.Errorf("actions[%d] = %q, want %q", i, fake.actions[i], want[i])
		}
	}
}
