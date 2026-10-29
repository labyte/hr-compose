package engine

import (
	"strings"
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

func TestStartReportsWhenServiceNotRunning(t *testing.T) {
	// systemctl start 返回 0 不代表进程活着（真机实测 command 路径不存在时照样返回 0）：
	// 复核没过的服务不能打印 OK、必须让命令报错，否则谎报成功且退出码为 0。
	fake := newDyingSys(map[string]map[string]string{"api.service": {
		"ActiveState": "activating", "SubState": "auto-restart", "MainPID": "0",
		"NRestarts": "3", "ExecMainCode": "exited", "ExecMainStatus": "203",
	}})
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/no/such/binary"},
		"redis": {Command: "/x/redis"},
	}}
	out := capturePsOutput(t)
	err := New(cfg, fake).Start()
	if err == nil {
		t.Fatal("start 返回 0 但进程没活着时 Start 必须返回错误")
	}
	if !strings.Contains(err.Error(), "api") || !strings.Contains(err.Error(), "203") {
		t.Errorf("错误应指明没起来的服务与退出码，got: %v", err)
	}
	if strings.Contains(err.Error(), "redis") {
		t.Errorf("redis 正常启动，不该被一起报错，got: %v", err)
	}
	table := out.String()
	if !strings.Contains(table, "start redis OK") {
		t.Errorf("只有复核通过的服务才该打印 OK，got:\n%s", table)
	}
	if strings.Contains(table, "start api OK") {
		t.Errorf("没起来的服务不该打印 OK，got:\n%s", table)
	}
}

func TestRestartReportsWhenServiceNotRunning(t *testing.T) {
	// restart 与 start 共用同一套复核：返回 0 但进程陷入 auto-restart 循环时报错、不打印 OK。
	fake := newDyingSys(map[string]map[string]string{"api.service": {
		"ActiveState": "activating", "SubState": "auto-restart", "MainPID": "0",
		"NRestarts": "1", "ExecMainCode": "exited", "ExecMainStatus": "203",
	}})
	cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/no/such/binary"}}}
	out := capturePsOutput(t)
	err := New(cfg, fake).Restart()
	if err == nil {
		t.Fatal("restart 返回 0 但进程没活着时 Restart 必须返回错误")
	}
	if !strings.Contains(err.Error(), "api") || !strings.Contains(err.Error(), "203") {
		t.Errorf("错误应指明没起来的服务与退出码，got: %v", err)
	}
	if strings.Contains(out.String(), "restart api OK") {
		t.Errorf("没起来的服务不该打印 OK，got:\n%s", out.String())
	}
	for _, a := range fake.actions {
		if a == "start api.service" {
			t.Errorf("Restart 不该退化成 start，actions = %v", fake.actions)
		}
	}
}
