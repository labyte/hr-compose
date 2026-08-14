package engine

import (
	"os"
	"path/filepath"
	"testing"

	"hr.compose/internal/config"
	"hr.compose/internal/systemctl"
	"hr.compose/internal/unit"
)

// fakeSys 记录调用序列，便于断言动作顺序。
type fakeSys struct {
	actions []string
}

func (f *fakeSys) Enable(u string) error  { f.actions = append(f.actions, "enable "+u); return nil }
func (f *fakeSys) Disable(u string) error { f.actions = append(f.actions, "disable "+u); return nil }
func (f *fakeSys) Start(u string) error   { f.actions = append(f.actions, "start "+u); return nil }
func (f *fakeSys) Stop(u string) error    { f.actions = append(f.actions, "stop "+u); return nil }
func (f *fakeSys) Restart(u string) error { f.actions = append(f.actions, "restart "+u); return nil }
func (f *fakeSys) DaemonReload() error {
	f.actions = append(f.actions, "daemon-reload")
	return nil
}
func (f *fakeSys) Show(u string) (map[string]string, error) {
	return fakeShowFields(), nil
}

func (f *fakeSys) ShowMany(units []string) (map[string]map[string]string, error) {
	m := make(map[string]map[string]string, len(units))
	for _, u := range units {
		m[u] = fakeShowFields()
	}
	return m, nil
}

// fakeShowFields 返回 fake 固定的 systemctl show 字段。
func fakeShowFields() map[string]string {
	return map[string]string{
		"ActiveState":   "active",
		"SubState":      "running",
		"UnitFileState": "enabled",
		"MainPID":       "123",
		"MemoryCurrent": "1048576",
	}
}

var _ systemctl.Client = (*fakeSys)(nil)

func TestOrderDependsFirst(t *testing.T) {
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", DependsOn: []string{"redis", "db"}},
		"redis": {Command: "/x/redis"},
		"db":    {Command: "/x/db"},
	}}
	e := New(cfg, &fakeSys{})
	got := e.order()
	idx := func(name string) int {
		for i, n := range got {
			if n == name {
				return i
			}
		}
		t.Fatalf("%s not in order %v", name, got)
		return -1
	}
	// 性质断言：所有依赖必须在 api 之前（拓扑序不唯一，不锁死具体顺序）
	if idx("redis") > idx("api") || idx("db") > idx("api") {
		t.Errorf("dependencies must come before api: %v", got)
	}
	if len(got) != 3 {
		t.Errorf("want 3 services, got %v", got)
	}
}

func TestUpWritesUnitsAndFollowsOrder(t *testing.T) {
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", DependsOn: []string{"redis"}},
		"redis": {Command: "/x/redis"},
	}}
	if err := New(cfg, fake).Up(); err != nil {
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
		t.Errorf("redis should start before api: %v", fake.actions)
	}
	for _, name := range []string{"api", "redis"} {
		if _, err := os.ReadFile(filepath.Join(UnitDir, name+".service")); err != nil {
			t.Errorf("%s.service not written: %v", name, err)
		}
	}
}

func TestDownStopsReverseOrderAndDeletesOnlyManaged(t *testing.T) {
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", DependsOn: []string{"redis"}},
		"redis": {Command: "/x/redis"},
	}}
	e := New(cfg, fake)
	// 先 up 生成托管文件
	if err := e.Up(); err != nil {
		t.Fatal(err)
	}
	if err := e.Down(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api", "redis"} {
		if _, err := os.Stat(filepath.Join(UnitDir, name+".service")); !os.IsNotExist(err) {
			t.Errorf("%s.service should be removed, err=%v", name, err)
		}
	}
	// 逆序停止：先停 api，再停 redis
	idx := func(s string) int {
		for i, a := range fake.actions {
			if a == s {
				return i
			}
		}
		return -1
	}
	if i, j := idx("stop api.service"), idx("stop redis.service"); i > j {
		t.Errorf("api should stop before redis: %v", fake.actions)
	}
}

func TestRemoveIfManagedProtectsForeignUnit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redis.service")
	if err := os.WriteFile(path, []byte("# original systemd unit\n[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeIfManaged(path); err == nil {
		t.Error("want error deleting foreign unit")
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("foreign unit should not be removed")
	}
}

func TestResolveUnknownService(t *testing.T) {
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	e := New(cfg, &fakeSys{})
	if _, err := e.resolve("nope"); err == nil {
		t.Error("want error for unknown service")
	}
}

func TestUpRefusesForeignUnit(t *testing.T) {
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	foreign := "# original systemd unit\n[Service]\nExecStart=/bin/true\n"
	if err := os.WriteFile(filepath.Join(UnitDir, "api.service"), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	if err := New(cfg, &fakeSys{}).Up(); err == nil {
		t.Fatal("存在非托管同名 unit 时 up 应报错")
	}
	if b, _ := os.ReadFile(filepath.Join(UnitDir, "api.service")); string(b) != foreign {
		t.Error("外部 unit 不应被改写")
	}
}

func TestUpOverwritesManagedUnit(t *testing.T) {
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	oldContent := unit.ManagedMark + " -- 旧版本\n[Service]\nExecStart=/old\n"
	if err := os.WriteFile(filepath.Join(UnitDir, "api.service"), []byte(oldContent), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	if err := New(cfg, &fakeSys{}).Up(); err != nil {
		t.Fatal(err)
	}
	g, err := unit.Generate("api", cfg.Services["api"])
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(UnitDir, "api.service")); string(b) != g.Content {
		t.Error("托管 unit 内容变化时应覆盖为最新内容")
	}
}

func TestUpSkipsUnchanged(t *testing.T) {
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	g, err := unit.Generate("api", cfg.Services["api"])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(UnitDir, "api.service"), []byte(g.Content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := New(cfg, &fakeSys{}).Up(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(UnitDir, "api.service")); string(b) != g.Content {
		t.Error("内容一致时不应改写")
	}
}
