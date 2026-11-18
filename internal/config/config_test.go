package config

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hr-compose.yml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  api:
    command: /opt/myapp/api
    depends_on: [redis]
  redis:
    command: /opt/redis/redis-server
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Services) != 2 {
		t.Fatalf("want 2 services, got %d", len(cfg.Services))
	}
	if got := cfg.Services["api"].EffectiveStdOutput(); got != "null" {
		t.Errorf("default std_output = %q, want null", got)
	}
}

func TestLoadRecordsServiceOrder(t *testing.T) {
	// 声明顺序与字母序相反（web > api > zzz），ServiceOrder 应按声明顺序记录
	p := writeFixture(t, `
name: testproj
services:
  web:
    command: /opt/myapp/web
    depends_on: [api]
  api:
    command: /opt/myapp/api
  zzz:
    command: /opt/myapp/zzz
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"web", "api", "zzz"}
	if len(cfg.ServiceOrder) != len(want) {
		t.Fatalf("ServiceOrder = %v, want %v", cfg.ServiceOrder, want)
	}
	for i := range want {
		if cfg.ServiceOrder[i] != want[i] {
			t.Errorf("ServiceOrder[%d] = %q, want %q", i, cfg.ServiceOrder[i], want[i])
		}
	}
}

func TestRejectUnknownField(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  api:
    command: /opt/myapp/api
    foobar: 1
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for unknown field")
	}
}

func TestRejectMissingCommand(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  api:
    working_dir: /opt/myapp
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for missing command")
	}
}

func TestRejectBadDependsOn(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  api:
    command: /opt/myapp/api
    depends_on: [not_exists]
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for undefined depends_on")
	}
}

func TestRejectDependsOnSelf(t *testing.T) {
	// 自依赖会在 unit 里生成 After=<自身>.service，systemd 报 ordering cycle 且删掉 start 作业，
	// 应提前在 Load 阶段明确拒绝。
	p := writeFixture(t, `
name: testproj
services:
  api:
    command: /opt/myapp/api
    depends_on: [api]
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for self depends_on")
	}
}

func TestRejectDependsOnCycle(t *testing.T) {
	// a <-> b 互相依赖：systemd 会静默删掉一个 start 作业，必须提前报错。
	p := writeFixture(t, `
name: testproj
services:
  a:
    command: /bin/a
    depends_on: [b]
  b:
    command: /bin/b
    depends_on: [a]
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for cyclic depends_on")
	}
}

func TestDependsOnCycleErrorReportsPath(t *testing.T) {
	// 报错信息应给出完整环路径，用户一眼就能看出要断哪条边。
	p := writeFixture(t, `
name: testproj
services:
  a:
    command: /bin/a
    depends_on: [c]
  b:
    command: /bin/b
    depends_on: [a]
  c:
    command: /bin/c
    depends_on: [b]
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "a -> c -> b -> a") {
		t.Errorf("error %q 应含环路径 a -> c -> b -> a", err)
	}
}

func TestAcceptAcyclicDependsOn(t *testing.T) {
	// 菱形依赖（无环）不应被误伤。
	p := writeFixture(t, `
name: testproj
services:
  a:
    command: /bin/a
  b:
    command: /bin/b
    depends_on: [a]
  c:
    command: /bin/c
    depends_on: [a]
  d:
    command: /bin/d
    depends_on: [b, c]
`)
	if _, err := Load(p); err != nil {
		t.Fatalf("无环依赖应合法, err=%v", err)
	}
}

func TestRejectRequiredDependsOnWithoutDependsOn(t *testing.T) {
	// depends_on_required: true 却未声明 depends_on：字段悬空，应报错引导修正。
	p := writeFixture(t, `
name: testproj
services:
  api:
    command: /opt/myapp/api
    depends_on_required: true
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for depends_on_required without depends_on")
	}
}

func TestLoadNewDependencyDelayFields(t *testing.T) {
	// depends_on_required 与 start_delay 齐备时应通过 Load 并正确落字段。
	p := writeFixture(t, `
name: testproj
services:
  api:
    command: /opt/myapp/api
  web:
    command: /opt/myapp/web
    depends_on: [api]
    depends_on_required: true
    start_delay: 5
    start_timeout: 120
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	web := cfg.Services["web"]
	if !web.DependsOnRequired {
		t.Error("DependsOnRequired = false, want true")
	}
	if web.StartDelay != 5 {
		t.Errorf("StartDelay = %d, want 5", web.StartDelay)
	}
	if got := web.EffectiveStartTimeout(); got != 120 {
		t.Errorf("EffectiveStartTimeout = %d, want 120", got)
	}
}

func TestRejectInvalidServiceName(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  "Api.Svc":
    command: /opt/myapp/api
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for invalid service name")
	}
}

func TestRejectEmptyServices(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services: {}
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for empty services")
	}
}

func TestRejectMissingName(t *testing.T) {
	// name 必填：缺省时所有命令（Load）都应报错，引导用户显式声明项目名。
	p := writeFixture(t, `
services:
  api:
    command: /opt/myapp/api
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for missing name")
	}
}

func TestRejectInvalidName(t *testing.T) {
	// name 会成为 unit 文件名前缀，字符集受限（含大写/点/空格均非法）。
	p := writeFixture(t, `
name: My.App
services:
  api:
    command: /opt/myapp/api
`)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for invalid name")
	}
}

func TestLoadSetsName(t *testing.T) {
	p := writeFixture(t, `
name: app1
services:
  api:
    command: /opt/myapp/api
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "app1" {
		t.Errorf("Name = %q, want app1", cfg.Name)
	}
}

func TestSanitizeName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"MyApp", "myapp"},
		{"My.App", "my-app"},
		{"a b c", "a-b-c"},
		{"already-ok_1", "already-ok_1"},
		{"你好世界", ""}, // 全非法 → 空
	} {
		if got := sanitizeName(tc.in); got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDefaultProjectName(t *testing.T) {
	// 取 yml 所在目录 basename 的 sanitize 值；目录名全非法时回退 my-project。
	dir := filepath.Join(t.TempDir(), "My App")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := defaultProjectName(filepath.Join(dir, "hr-compose.yml")); got != "my-app" {
		t.Errorf("defaultProjectName = %q, want my-app", got)
	}
	bad := filepath.Join(t.TempDir(), "你好世界")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := defaultProjectName(filepath.Join(bad, "hr-compose.yml")); got != "my-project" {
		t.Errorf("defaultProjectName(全非法目录) = %q, want my-project", got)
	}
}

func TestQuotedNullStdOutput(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  worker:
    command: /opt/worker
    std_output: "null"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveStdOutput(); got != "null" {
		t.Errorf("EffectiveStdOutput = %q, want \"null\"", got)
	}
}

func TestUnquotedNullDefaultsToNull(t *testing.T) {
	// 未加引号的 null 是 YAML null 字面量，等价于"未配置"，按默认 null（丢弃输出）处理。
	p := writeFixture(t, `
name: testproj
services:
  worker:
    command: /opt/worker
    std_output: null
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveStdOutput(); got != "null" {
		t.Errorf("EffectiveStdOutput = %q, want \"null\"", got)
	}
}

func TestNoneStdOutputAlias(t *testing.T) {
	// std_output: none 是旧版本推荐的写法，兼容保留，同样归一为 "null"。
	p := writeFixture(t, `
name: testproj
services:
  worker:
    command: /opt/worker
    std_output: none
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveStdOutput(); got != "null" {
		t.Errorf("EffectiveStdOutput = %q, want \"null\"", got)
	}
}

func TestDefaultRestart(t *testing.T) {
	// restart / restart_sec 未配置时走代码默认：always / 5。
	p := writeFixture(t, `
name: testproj
services:
  worker:
    command: /opt/worker
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	svc := cfg.Services["worker"]
	if got := svc.EffectiveRestart(); got != "always" {
		t.Errorf("EffectiveRestart = %q, want always", got)
	}
	if got := svc.EffectiveRestartSec(); got != 5 {
		t.Errorf("EffectiveRestartSec = %d, want 5", got)
	}
}

func TestExplicitRestartPassthrough(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  worker:
    command: /opt/worker
    restart: on-failure
    restart_sec: 10
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	svc := cfg.Services["worker"]
	if got := svc.EffectiveRestart(); got != "on-failure" {
		t.Errorf("EffectiveRestart = %q, want on-failure", got)
	}
	if got := svc.EffectiveRestartSec(); got != 10 {
		t.Errorf("EffectiveRestartSec = %d, want 10", got)
	}
}

func TestDefaultStartTimeout(t *testing.T) {
	// start_timeout 未配置时走代码默认 90（systemd 默认值）。
	p := writeFixture(t, `
name: testproj
services:
  worker:
    command: /opt/worker
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveStartTimeout(); got != 90 {
		t.Errorf("EffectiveStartTimeout = %d, want 90", got)
	}
}

func TestExplicitStartTimeoutPassthrough(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  worker:
    command: /opt/worker
    start_timeout: 300
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveStartTimeout(); got != 300 {
		t.Errorf("EffectiveStartTimeout = %d, want 300", got)
	}
}

func TestEffectiveUserPassthrough(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  worker:
    command: /opt/worker
    user: appuser
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveUser(); got != "appuser" {
		t.Errorf("EffectiveUser = %q, want appuser", got)
	}
}

func TestEffectiveUserDefaultsToResolver(t *testing.T) {
	// user 未配置时自动注入 userResolver 的结果（执行 up 的真实用户）。
	old := userResolver
	defer func() { userResolver = old }()
	userResolver = func() string { return "alice" }
	p := writeFixture(t, `
name: testproj
services:
  worker:
    command: /opt/worker
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["worker"].EffectiveUser(); got != "alice" {
		t.Errorf("EffectiveUser = %q, want alice", got)
	}
}

func TestUserResolverPrefersSudoUser(t *testing.T) {
	// sudo 下进程是 root，真实用户在 SUDO_USER 环境变量里。
	old, had := os.LookupEnv("SUDO_USER")
	t.Cleanup(func() {
		if had {
			os.Setenv("SUDO_USER", old)
		} else {
			os.Unsetenv("SUDO_USER")
		}
	})
	os.Setenv("SUDO_USER", "bob")
	if got := userResolver(); got != "bob" {
		t.Errorf("userResolver = %q, want bob", got)
	}
}

func TestParseDescription(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  api:
    description: 主业务 API 服务
    command: /opt/myapp/api
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	svc := cfg.Services["api"]
	if svc.Description != "主业务 API 服务" {
		t.Errorf("Description = %q, want 主业务 API 服务", svc.Description)
	}
	if got := svc.EffectiveDescription("api"); got != "主业务 API 服务" {
		t.Errorf("EffectiveDescription = %q, want 配置的描述", got)
	}
}

func TestEffectiveDescriptionDefault(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  api:
    command: /opt/myapp/api
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["api"].EffectiveDescription("api"); got != "hr-compose service api" {
		t.Errorf("EffectiveDescription = %q, want 默认值", got)
	}
}

func TestRejectNewlineInCommand(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  api:
    command: "/bin/true\nExecStart=/bin/evil"
`)
	if _, err := Load(p); err == nil {
		t.Fatal("command 含换行应被拒绝（unit 注入）")
	}
}

func TestRejectNewlineInEnvironment(t *testing.T) {
	p := writeFixture(t, `
name: testproj
services:
  api:
    command: /opt/myapp/api
    environment:
      - "FOO=1\nExecStart=/bin/evil"
`)
	if _, err := Load(p); err == nil {
		t.Fatal("environment 含换行应被拒绝（unit 注入）")
	}
}

func TestRejectNewlineInAllPassthroughFields(t *testing.T) {
	// 凡是值被原样拼进 unit 行的字段，换行都等于往 unit 里另起一行注入 systemd 指令：
	// description 带换行可在 [Unit] 段塞出 [Service]/ExecStartPre=（真机验证过跨段注入成立）。
	// 这里逐字段覆盖，新增透传字段时须同步加进来（config.passthroughValues 是唯一登记表）。
	fields := []string{
		"description", "working_dir", "user", "group", "restart",
		"stop_signal", "memory_max", "cpu_quota", "std_output", "log_file",
	}
	for _, f := range fields {
		p := writeFixture(t, `
name: testproj
services:
  api:
    command: /opt/myapp/api
    `+f+`: "合法值\nExecStartPre=/bin/touch /tmp/proof"
`)
		if _, err := Load(p); err == nil {
			t.Errorf("%s 含换行应被拒绝（unit 注入）", f)
		}
	}
}

func TestAcceptMultilineFreeValues(t *testing.T) {
	// 反向用例：常规写法（含空格、中文、路径、systemd 取值语法）不应被换行校验误伤
	p := writeFixture(t, `
name: testproj
services:
  api:
    description: 主业务 API 服务 (v2)
    command: /opt/myapp/api --config /etc/myapp/api.conf
    working_dir: /opt/myapp
    user: appuser
    group: appgroup
    restart: on-failure
    stop_signal: SIGTERM
    memory_max: 500M
    cpu_quota: 80%
    std_output: append:/data/logs/api.log
    log_file: /data/logs/api.log
    environment:
      - DB_ADDR=127.0.0.1:3306
`)
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
}

// file:/append: 的相对路径在 clean/logs（按当前目录解释）与 systemd 之间解释不一致，
// 会截断/查看错文件，Load 阶段直接拒绝；绝对路径与 journal/null 不受影响。
func TestLoadRejectsRelativeStdOutputPath(t *testing.T) {
	for _, v := range []string{"file:logs/api.log", "append:api.log", "file:", "append:"} {
		p := writeFixture(t, "name: testproj\nservices:\n  api:\n    command: /x/api\n    std_output: "+v+"\n")
		if _, err := Load(p); err == nil {
			t.Errorf("std_output %q 相对路径应报错", v)
		}
	}
	for _, v := range []string{"file:/var/log/api.log", "append:/var/log/api.log", "journal", "null", ""} {
		p := writeFixture(t, "name: testproj\nservices:\n  api:\n    command: /x/api\n    std_output: "+v+"\n")
		if _, err := Load(p); err != nil {
			t.Errorf("std_output %q 应合法, err=%v", v, err)
		}
	}
}

// log_file 由 logs 命令按执行时当前目录解释相对路径，换目录跑 logs 会看错文件，Load 阶段拒绝。
func TestLoadRejectsRelativeLogFile(t *testing.T) {
	p := writeFixture(t, "name: testproj\nservices:\n  api:\n    command: /x/api\n    log_file: logs/api.log\n")
	if _, err := Load(p); err == nil {
		t.Errorf("log_file 相对路径应报错")
	}
	p = writeFixture(t, "name: testproj\nservices:\n  api:\n    command: /x/api\n    log_file: /var/log/app/*.log\n")
	if _, err := Load(p); err != nil {
		t.Errorf("log_file 绝对 glob 应合法, err=%v", err)
	}
}

// withPathResolvers 覆盖路径解析依赖（cwd 与 home），t.Cleanup 自动还原。
func withPathResolvers(t *testing.T, cwd, home string, homeErr error) {
	t.Helper()
	oldCwd, oldHome := cwdResolver, homeDirResolver
	t.Cleanup(func() { cwdResolver, homeDirResolver = oldCwd, oldHome })
	cwdResolver = func() (string, error) { return cwd, nil }
	homeDirResolver = func() (string, error) { return home, homeErr }
}

// writeConfigAt 在 dir 下写一份最小合法编排文件，返回其路径。
func writeConfigAt(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, defaultFileName)
	if err := os.WriteFile(p, []byte("name: t\nservices:\n  a:\n    command: /bin/true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolvePathExplicitWins(t *testing.T) {
	withPathResolvers(t, t.TempDir(), t.TempDir(), nil)
	explicit := filepath.Join(t.TempDir(), "custom.yml")
	got, err := ResolvePath(explicit)
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if got != explicit {
		t.Errorf("ResolvePath = %q, want %q（--file 应原样使用且不回退）", got, explicit)
	}
}

func TestResolvePathPrefersCurrentDir(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	want := writeConfigAt(t, cwd)
	writeConfigAt(t, home)
	withPathResolvers(t, cwd, home, nil)
	got, err := ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if got != want {
		t.Errorf("ResolvePath = %q, want %q（当前目录优先）", got, want)
	}
}

func TestResolvePathFallsBackToHome(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	want := writeConfigAt(t, home)
	withPathResolvers(t, cwd, home, nil)
	got, err := ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if got != want {
		t.Errorf("ResolvePath = %q, want %q（当前目录缺失应回退用户主目录）", got, want)
	}
}

func TestResolvePathNotFoundReportsSearchedLocations(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	withPathResolvers(t, cwd, home, nil)
	_, err := ResolvePath("")
	if err == nil {
		t.Fatal("两处都没有编排文件时应报错")
	}
	for _, want := range []string{filepath.Join(cwd, defaultFileName), filepath.Join(home, defaultFileName), "--file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息 %q 应包含 %q", err, want)
		}
	}
}

func TestResolvePathHomeUnavailable(t *testing.T) {
	cwd := t.TempDir()
	withPathResolvers(t, cwd, "", errors.New("无主目录"))
	_, err := ResolvePath("")
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), filepath.Join(cwd, defaultFileName)) {
		t.Errorf("错误信息 %q 应包含当前目录路径", err)
	}
	if strings.Contains(err.Error(), "用户主目录") {
		t.Errorf("主目录不可用时不应列出主目录路径: %v", err)
	}
}

func TestResolvePathIgnoresDirectoryNamedConfig(t *testing.T) {
	// 当前目录下同名的是目录而非文件，应继续回退，而不是把它当编排文件去解析。
	cwd, home := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, defaultFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	want := writeConfigAt(t, home)
	withPathResolvers(t, cwd, home, nil)
	got, err := ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if got != want {
		t.Errorf("ResolvePath = %q, want %q（同名目录不算配置文件）", got, want)
	}
}

func TestResolvePathHomeSameAsCwd(t *testing.T) {
	// 在主目录下执行且没有配置时，当前目录与主目录是同一路径，错误信息不应把同一路径列两遍。
	home := t.TempDir()
	withPathResolvers(t, home, home, nil)
	_, err := ResolvePath("")
	if err == nil {
		t.Fatal("want error")
	}
	if got := strings.Count(err.Error(), filepath.Join(home, defaultFileName)); got != 1 {
		t.Errorf("同一路径应只出现一次，实际 %d 次: %v", got, err)
	}
}

// --- homeDirResolver 的真实取值口径（上面的 ResolvePath 用例都把它替换成了桩）---

// setHomeEnv 同时覆盖 SUDO_USER 与 HOME，t.Setenv 自动还原。
// 注：t.Setenv 无法真正 unset，但代码按 os.Getenv 是否为空串判断，置空等价于未设。
func setHomeEnv(t *testing.T, sudoUser, home string) {
	t.Helper()
	t.Setenv("SUDO_USER", sudoUser)
	t.Setenv("HOME", home)
}

func TestHomeDirResolverPrefersSudoUser(t *testing.T) {
	// sudo 下进程是 root、HOME 已被重置为 /root，主目录必须按 SUDO_USER 查 passwd，
	// 否则回退去找的是 /root/hr-compose.yml，用户放在自己家目录的配置永远命中不了。
	cur, err := user.Current()
	if err != nil {
		t.Skipf("无法获取当前用户: %v", err)
	}
	u, err := user.Lookup(cur.Username)
	if err != nil || u.HomeDir == "" {
		t.Skipf("用户 %q 不在 passwd 中，无法断言 Lookup 结果: %v", cur.Username, err)
	}
	setHomeEnv(t, cur.Username, filepath.Join(t.TempDir(), "root")) // 故意给 HOME 塞个假值，证明取值不来自 $HOME
	home, err := homeDirResolver()
	if err != nil {
		t.Fatalf("homeDirResolver: %v", err)
	}
	if home != u.HomeDir {
		t.Errorf("homeDirResolver = %q, want %q（应按 SUDO_USER 查 passwd）", home, u.HomeDir)
	}
}

func TestHomeDirResolverWithoutSudoUsesHomeEnv(t *testing.T) {
	home := t.TempDir()
	setHomeEnv(t, "", home)
	got, err := homeDirResolver()
	if err != nil {
		t.Fatalf("homeDirResolver: %v", err)
	}
	if got != home {
		t.Errorf("homeDirResolver = %q, want %q（非 sudo 下取 $HOME）", got, home)
	}
}

func TestHomeDirResolverUnknownSudoUserFallsBackToHomeEnv(t *testing.T) {
	// SUDO_USER 指向 passwd 里查不到的用户（账户已删等）时退回 $HOME，不因查不到用户直接报错。
	home := t.TempDir()
	setHomeEnv(t, "no-such-user-for-test", home)
	got, err := homeDirResolver()
	if err != nil {
		t.Fatalf("homeDirResolver: %v", err)
	}
	if got != home {
		t.Errorf("homeDirResolver = %q, want %q（查不到 SUDO_USER 应退回 $HOME）", got, home)
	}
}

func TestInitInHomeDirResolvesFromAnyDir(t *testing.T) {
	// 完整链路：在登录目录执行 init 生成一份配置，之后在任意目录执行命令都回退命中它，
	// 且命中的文件可直接 Load（模板本身就是合法配置）。
	root := t.TempDir()
	home := filepath.Join(root, "loginhome") // 登录目录（放全局配置）
	cwd := filepath.Join(root, "workdir")    // 执行命令时所在的目录，本身没有配置
	for _, d := range []string{home, cwd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	withPathResolvers(t, cwd, home, nil)

	p := filepath.Join(home, DefaultPath())
	if err := Init(p); err != nil {
		t.Fatalf("Init(%s): %v", p, err)
	}
	got, err := ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if got != p {
		t.Errorf("ResolvePath = %q, want 登录目录下的 %q", got, p)
	}
	cfg, err := Load(got)
	if err != nil {
		t.Fatalf("回退命中的配置应能直接加载: %v", err)
	}
	if len(cfg.Services) != 2 {
		t.Errorf("模板应有 2 个服务，实际 %d", len(cfg.Services))
	}
	// 默认项目名取配置文件所在目录（登录目录）的 basename，不是执行命令时所在的目录名。
	if cfg.Name != "loginhome" {
		t.Errorf("name = %q, want loginhome（取登录目录名，非当前目录名）", cfg.Name)
	}
}
