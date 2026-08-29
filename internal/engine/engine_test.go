package engine

import (
	"os"
	"path/filepath"
	"strings"
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
func (f *fakeSys) ClearJournal() error {
	f.actions = append(f.actions, "clear-journal")
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
// ExecMainStartTimestampMonotonic=600s（开机后 10 分钟启动主进程，配合 runPs 的 1h 开机时长得出 UPTIME=50m）。
func fakeShowFields() map[string]string {
	return map[string]string{
		"ActiveState":                     "active",
		"SubState":                        "running",
		"LoadState":                       "loaded",
		"UnitFileState":                   "enabled",
		"MainPID":                         "123",
		"MemoryCurrent":                   "1048576",
		"ExecMainStartTimestampMonotonic": "600000000",
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
	got := e.order(e.allNames())
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

func TestOrderFollowsDeclarationOrderWhenNoDeps(t *testing.T) {
	// 无依赖关系时按 yml 声明顺序（ServiceOrder）输出，而非按名称排序
	cfg := &config.Config{
		Services: map[string]*config.Service{
			"api": {Command: "/x/api"},
			"web": {Command: "/x/web"},
		},
		ServiceOrder: []string{"web", "api"},
	}
	e := New(cfg, &fakeSys{})
	got := e.order(e.allNames())
	want := []string{"web", "api"}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestOrderDepsRespectedWithinDeclarationOrder(t *testing.T) {
	// 声明顺序中 web 在前，但 web 依赖 api → api 仍必须先于 web
	cfg := &config.Config{
		Services: map[string]*config.Service{
			"api": {Command: "/x/api"},
			"web": {Command: "/x/web", DependsOn: []string{"api"}},
		},
		ServiceOrder: []string{"web", "api"},
	}
	e := New(cfg, &fakeSys{})
	got := e.order(e.allNames())
	idx := func(name string) int {
		for i, n := range got {
			if n == name {
				return i
			}
		}
		t.Fatalf("%s not in order %v", name, got)
		return -1
	}
	if idx("api") > idx("web") {
		t.Errorf("api should come before web (web depends on api): %v", got)
	}
	if len(got) != 2 {
		t.Errorf("want 2 services, got %v", got)
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

func TestUpFollowsDeclarationOrderWithoutDeps(t *testing.T) {
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := &fakeSys{}
	// 声明顺序 web 在 api 前，且二者无依赖 → 按声明顺序 start
	cfg := &config.Config{
		Services: map[string]*config.Service{
			"api": {Command: "/x/api"},
			"web": {Command: "/x/web"},
		},
		ServiceOrder: []string{"web", "api"},
	}
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
	if i, j := idx("start web.service"), idx("start api.service"); i > j {
		t.Errorf("web should start before api (declaration order, no deps): %v", fake.actions)
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

func TestResolveMultipleAndDedup(t *testing.T) {
	// 多个名字：校验存在、去重并保持输入顺序（无依赖时直接按输入顺序输出）
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api"},
		"redis": {Command: "/x/redis"},
	}}
	got, err := New(cfg, &fakeSys{}).resolve("redis", "api", "redis")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"redis", "api"}
	if len(got) != len(want) {
		t.Fatalf("resolve = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("resolve[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveUnknownMultiple(t *testing.T) {
	// 未定义的名字一次全部列出
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	_, err := New(cfg, &fakeSys{}).resolve("nope", "foo")
	if err == nil {
		t.Fatal("未知服务名应报错")
	}
	for _, n := range []string{"nope", "foo"} {
		if !strings.Contains(err.Error(), n) {
			t.Errorf("错误信息应列出未定义的服务 %q: %v", n, err)
		}
	}
}

func TestOrderSubsetIgnoresDepsOutside(t *testing.T) {
	// 子集内服务依赖子集外服务时，子集外依赖忽略（视为已满足），
	// 使 `start web` 只返回 [web] 而非连带其依赖。
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
		"web": {Command: "/x/web", DependsOn: []string{"api"}},
	}}
	got := New(cfg, &fakeSys{}).order([]string{"web"})
	if len(got) != 1 || got[0] != "web" {
		t.Errorf("order([web]) = %v, want 仅 [web]", got)
	}
}

func TestUpSubsetIncludesDependencies(t *testing.T) {
	// up 指定服务时自动带上其 depends_on 依赖（递归），且不触碰无关服务
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", DependsOn: []string{"redis"}},
		"redis": {Command: "/x/redis"},
		"web":   {Command: "/x/web"}, // 与 api 无关，不应被启动
	}}
	if err := New(cfg, fake).Up("api"); err != nil {
		t.Fatal(err)
	}
	idx := func(s string) int {
		for i, a := range fake.actions {
			if a == s {
				return i
			}
		}
		return -1
	}
	// 闭包：api 的依赖 redis 一并写入并先启动
	if i, j := idx("start redis.service"), idx("start api.service"); i == -1 || j == -1 || i > j {
		t.Errorf("redis 应先于 api 启动（Up(api) 带上依赖）: %v", fake.actions)
	}
	for _, name := range []string{"api", "redis"} {
		if _, err := os.Stat(filepath.Join(UnitDir, name+".service")); err != nil {
			t.Errorf("%s.service 应被写入: %v", name, err)
		}
	}
	// 无关服务 web 不被写入也不被启动
	if _, err := os.Stat(filepath.Join(UnitDir, "web.service")); !os.IsNotExist(err) {
		t.Errorf("Up(api) 不应写入无关的 web.service")
	}
	if idx("start web.service") != -1 {
		t.Errorf("Up(api) 不应启动无关的 web: %v", fake.actions)
	}
}

func TestDownSubsetRemovesOnlyNamed(t *testing.T) {
	// down 指定服务只清理指定的，不连带其依赖；依赖的 unit 保留
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := &fakeSys{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api":   {Command: "/x/api", DependsOn: []string{"redis"}},
		"redis": {Command: "/x/redis"},
	}}
	e := New(cfg, fake)
	if err := e.Up(); err != nil {
		t.Fatal(err)
	}
	if err := e.Down("api"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(UnitDir, "api.service")); !os.IsNotExist(err) {
		t.Errorf("api.service 应被删除: %v", err)
	}
	if _, err := os.Stat(filepath.Join(UnitDir, "redis.service")); err != nil {
		t.Errorf("Down(api) 不应删除依赖 redis.service: %v", err)
	}
	if containsAction(fake.actions, "stop redis.service") {
		t.Errorf("Down(api) 不应 stop 依赖 redis: %v", fake.actions)
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
	g, err := unit.Generate("api", cfg.Services["api"], "")
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
	g, err := unit.Generate("api", cfg.Services["api"], "")
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

func TestUnitNamePrefix(t *testing.T) {
	// cfg.Name（name:）非空时：unit 文件名、systemctl 动作、down 清理全部带 project 前缀，
	// 使不同目录的同名服务互不覆盖。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := &fakeSys{}
	cfg := &config.Config{
		Name: "myapp",
		Services: map[string]*config.Service{
			"api":   {Command: "/x/api", DependsOn: []string{"redis"}},
			"redis": {Command: "/x/redis"},
		},
	}
	e := New(cfg, fake)
	if err := e.Up(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"myapp-api", "myapp-redis"} {
		if _, err := os.Stat(filepath.Join(UnitDir, name+".service")); err != nil {
			t.Errorf("应写入 %s.service: %v", name, err)
		}
	}
	// 依赖关系写入带前缀的 After/Wants
	g, err := unit.Generate("api", cfg.Services["api"], "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Content, "After=myapp-redis.service") {
		t.Errorf("After 应带 project 前缀\n%s", g.Content)
	}
	// systemctl 动作使用带前缀的 unit 名
	for _, want := range []string{
		"enable myapp-redis.service", "start myapp-redis.service",
		"enable myapp-api.service", "start myapp-api.service",
	} {
		found := false
		for _, a := range fake.actions {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("动作序列缺少 %q，got %v", want, fake.actions)
		}
	}
	// down 清理带前缀的 unit，不残留
	if err := e.Down(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"myapp-api", "myapp-redis"} {
		if _, err := os.Stat(filepath.Join(UnitDir, name+".service")); !os.IsNotExist(err) {
			t.Errorf("%s.service 应被删除", name)
		}
	}
}
