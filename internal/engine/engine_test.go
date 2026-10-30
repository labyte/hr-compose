package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hr.compose/internal/config"
	"hr.compose/internal/systemctl"
	"hr.compose/internal/unit"
)

// TestMain 把 up 复核的等待改成空操作：单测里 systemctl 是 fake、状态即时给定，
// 真睡 300ms×轮数只会拖慢测试而不改变结论（复核的判断逻辑仍按 fake 状态原样断言）。
func TestMain(m *testing.M) {
	old := sleepFunc
	sleepFunc = func(time.Duration) {}
	code := m.Run()
	sleepFunc = old
	os.Exit(code)
}

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

// stateSys 在 fakeSys 基础上按 unit 指定状态，用于验证 up 的 start/restart 分流与启动复核：
// states 是"写 unit 之前"的状态（Up 据此决定 start 还是 restart）；调用过 Start/Restart 之后
// 改用 after（未指定的服务视为启动成功、running），对应真实世界里"start 之后进程就跑起来了"，
// 这样只关心动作序列的用例不会被收尾复核误伤。
type stateSys struct {
	fakeSys
	states  map[string]string
	after   map[string]map[string]string
	started bool
}

func (s *stateSys) show(u string) map[string]string {
	f := fakeShowFields()
	if s.started {
		if custom, ok := s.after[u]; ok {
			for k, v := range custom {
				f[k] = v
			}
		}
		return f
	}
	if st, ok := s.states[u]; ok {
		f["ActiveState"] = st
		f["SubState"] = "dead"
	}
	return f
}

func (s *stateSys) Show(u string) (map[string]string, error) { return s.show(u), nil }

func (s *stateSys) ShowMany(units []string) (map[string]map[string]string, error) {
	m := make(map[string]map[string]string, len(units))
	for _, u := range units {
		m[u] = s.show(u)
	}
	return m, nil
}

func (s *stateSys) Start(u string) error {
	s.started = true
	s.actions = append(s.actions, "start "+u)
	return nil
}

func (s *stateSys) Restart(u string) error {
	s.started = true
	s.actions = append(s.actions, "restart "+u)
	return nil
}

// failStartSys 让指定 unit 的 start 失败，用于验证 up 的部分失败语义。
type failStartSys struct {
	fakeSys
	fail map[string]bool
}

func (f *failStartSys) Start(u string) error {
	if f.fail[u] {
		f.actions = append(f.actions, "start "+u)
		return errors.New("模拟启动失败（如 ExecStart 路径不存在）")
	}
	return f.fakeSys.Start(u)
}

// hasAction 判断动作序列中是否有某条动作（整条精确匹配，避免 restart 被当成 start）。
func hasAction(actions []string, want string) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}

// actionIndex 返回动作首次出现的位置，不存在时返回 -1。
func actionIndex(actions []string, want string) int {
	for i, a := range actions {
		if a == want {
			return i
		}
	}
	return -1
}

// writeUnitFile 按给定配置先把 unit 装上（模拟上一次 up 的产物），返回生成内容。
func writeUnitFile(t *testing.T, name string, svc *config.Service, project string) string {
	t.Helper()
	g, err := unit.Generate(name, svc, project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(UnitDir, g.UnitPath), []byte(g.Content), 0o644); err != nil {
		t.Fatal(err)
	}
	return g.Content
}

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

func TestUpRestartsRunningServiceWhenUnitChanged(t *testing.T) {
	// 真机实证过：systemctl start 对 active 的 unit 是 no-op，改完配置只 reload 不重启，
	// 运行中的进程仍是旧配置。unit 内容变化 + 服务在跑 → 必须 restart。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	writeUnitFile(t, "api", &config.Service{Command: "/x/old"}, "")
	fake := &stateSys{states: map[string]string{"api.service": "active"}}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/new"},
	}}
	if err := New(cfg, fake).Up(); err != nil {
		t.Fatal(err)
	}
	if !hasAction(fake.actions, "restart api.service") {
		t.Errorf("unit 内容变化且服务在运行时应 restart，动作序列 %v", fake.actions)
	}
	if hasAction(fake.actions, "start api.service") {
		t.Errorf("内容变化的运行中服务不应只 start，动作序列 %v", fake.actions)
	}
	if i, j := actionIndex(fake.actions, "daemon-reload"), actionIndex(fake.actions, "restart api.service"); i > j {
		t.Errorf("restart 必须在 daemon-reload 之后（新 unit 内容先加载）: %v", fake.actions)
	}
}

func TestUpStartsStoppedServiceWhenUnitChanged(t *testing.T) {
	// 服务没在跑（inactive/failed/未安装）时不需要 restart，start 即可生效，避免多余的一次停止-启动。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	for _, state := range []string{"inactive", "failed", ""} {
		UnitDir = t.TempDir()
		writeUnitFile(t, "api", &config.Service{Command: "/x/old"}, "")
		fake := &stateSys{states: map[string]string{"api.service": state}}
		cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/x/new"}}}
		if err := New(cfg, fake).Up(); err != nil {
			t.Fatal(err)
		}
		if !hasAction(fake.actions, "start api.service") || hasAction(fake.actions, "restart api.service") {
			t.Errorf("ActiveState=%q 时应 start 而非 restart: %v", state, fake.actions)
		}
	}
}

func TestUpDoesNotRestartUnchangedRunningService(t *testing.T) {
	// 内容没变（重复 up）时不得 restart —— restart 会把没改过的服务白白重启一次。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/x/api"}}}
	writeUnitFile(t, "api", cfg.Services["api"], "")
	fake := &stateSys{states: map[string]string{"api.service": "active"}}
	if err := New(cfg, fake).Up(); err != nil {
		t.Fatal(err)
	}
	if hasAction(fake.actions, "restart api.service") {
		t.Errorf("内容未变时不应 restart: %v", fake.actions)
	}
	if !hasAction(fake.actions, "start api.service") {
		t.Errorf("内容未变时应保持原样只 start: %v", fake.actions)
	}
}

func TestUpFirstInstallStartsRunningUnit(t *testing.T) {
	// 首次安装（文件原本不存在）即使 systemd 里该 unit 名字已在运行，也只 start 不 restart，
	// 语义是"安装并启动"，不是"配置变更需重启"。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := &stateSys{states: map[string]string{"api.service": "active"}}
	cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/x/api"}}}
	if err := New(cfg, fake).Up(); err != nil {
		t.Fatal(err)
	}
	if hasAction(fake.actions, "restart api.service") {
		t.Errorf("首次安装不应 restart: %v", fake.actions)
	}
	if !hasAction(fake.actions, "start api.service") {
		t.Errorf("首次安装应 start: %v", fake.actions)
	}
}

func TestUpContinuesAfterFailureAndReports(t *testing.T) {
	// 单个服务启动失败（ExecStart 路径不存在等）：后面的服务照常安装启动，不因此中断；
	// 但最终必须返回错误并把失败服务列出来，不能只打 "up xxx 已启动" 谎报成功。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	fake := &failStartSys{fail: map[string]bool{"api.service": true}}
	cfg := &config.Config{
		Services: map[string]*config.Service{
			"api": {Command: "/nonexistent/api"},
			"web": {Command: "/x/web"},
		},
		ServiceOrder: []string{"api", "web"},
	}
	err := New(cfg, fake).Up()
	if err == nil {
		t.Fatal("有服务启动失败时 up 应返回错误")
	}
	if !strings.Contains(err.Error(), "api") {
		t.Errorf("错误应点出失败的服务名，got %v", err)
	}
	if !hasAction(fake.actions, "start web.service") {
		t.Errorf("api 失败后 web 仍应被启动: %v", fake.actions)
	}
	if strings.Contains(err.Error(), "up web OK") {
		t.Errorf("错误信息不该把成功服务也算进去: %v", err)
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

// brokenThenOKSys 让前 remain 次状态查询（批量与逐服务都计数）失败，之后恢复正常，
// 模拟 systemctl 瞬时不可用（连不上总线、超时等）。
type brokenThenOKSys struct {
	stateSys
	remain int
}

func newBrokenThenOKSys(states map[string]string, remain int) *brokenThenOKSys {
	return &brokenThenOKSys{stateSys: stateSys{states: states}, remain: remain}
}

// broken 消费一次"失败额度"，用完即恢复正常。
func (s *brokenThenOKSys) broken() bool {
	if s.remain > 0 {
		s.remain--
		return true
	}
	return false
}

func (s *brokenThenOKSys) Show(u string) (map[string]string, error) {
	if s.broken() {
		return nil, errors.New("systemctl show " + u + ": exit status 1\nFailed to connect to bus: Host is down")
	}
	return s.stateSys.Show(u)
}

func (s *brokenThenOKSys) ShowMany(units []string) (map[string]map[string]string, error) {
	if s.broken() {
		return nil, errors.New("systemctl show: exit status 1\nFailed to connect to bus: Host is down")
	}
	return s.stateSys.ShowMany(units)
}

// showFailSys 状态查询恒失败（批量与逐服务都失败），模拟 systemctl 整体不可用。
type showFailSys struct {
	fakeSys
}

func (s *showFailSys) Show(u string) (map[string]string, error) {
	return nil, errors.New("systemctl show " + u + ": exit status 1\nFailed to connect to bus: Host is down")
}

func (s *showFailSys) ShowMany(units []string) (map[string]map[string]string, error) {
	return nil, errors.New("systemctl show: exit status 1\nFailed to connect to bus: Host is down")
}

func TestUpRestartsWhenStateQueryTransientlyFails(t *testing.T) {
	// unit 内容已变但运行状态查不到时，不能退回 start：start 对 active 的 unit 是 no-op，
	// 新配置进不了正在跑的进程，而 up 仍会打印"已启动"——把静默失败说成成功。
	// restart 在两种情况下都得到"运行中"的终态（停着的服务 restart 即启动），是保守正确的选择。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	writeUnitFile(t, "api", &config.Service{Command: "/x/old"}, "")
	// 写入前服务在运行；前两次状态查询（ShowMany + 回退的 Show）失败，之后恢复
	fake := newBrokenThenOKSys(map[string]string{"api.service": "active"}, 2)
	cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/x/new"}}}
	out := capturePsOutput(t)
	if err := New(cfg, fake).Up(); err != nil {
		t.Fatal(err)
	}
	if !hasAction(fake.actions, "restart api.service") {
		t.Errorf("查不到运行状态时应按 restart 处理，动作序列 %v", fake.actions)
	}
	if hasAction(fake.actions, "start api.service") {
		t.Errorf("不应退回 start（对 active unit 是 no-op，新配置不生效），动作序列 %v", fake.actions)
	}
	if s := out.String(); !strings.Contains(s, "警告") || !strings.Contains(s, "restart") {
		t.Errorf("查询失败应如实告警并说明改用 restart，输出:\n%s", s)
	}
}

func TestUpRestartAndReportWhenStateQueryUnavailable(t *testing.T) {
	// 状态一直查不到时：动作仍按 restart（保守正确），但 up 必须把"复核不了"如实报错，
	// 不能只打印"已启动"就退出 0。
	old := UnitDir
	UnitDir = t.TempDir()
	defer func() { UnitDir = old }()

	writeUnitFile(t, "api", &config.Service{Command: "/x/old"}, "")
	fake := &showFailSys{}
	cfg := &config.Config{Services: map[string]*config.Service{"api": {Command: "/x/new"}}}
	capturePsOutput(t)
	err := New(cfg, fake).Up()
	if err == nil {
		t.Fatal("状态复核查不到时 up 应返回错误")
	}
	if !strings.Contains(err.Error(), "复核服务状态") {
		t.Errorf("错误应说明是复核环节查不到状态，got: %v", err)
	}
	if !hasAction(fake.actions, "restart api.service") {
		t.Errorf("查不到运行状态时应按 restart 处理，动作序列 %v", fake.actions)
	}
	if hasAction(fake.actions, "start api.service") {
		t.Errorf("不应退回 start，动作序列 %v", fake.actions)
	}
}
