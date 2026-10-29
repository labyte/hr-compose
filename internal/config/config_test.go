package config

import (
	"os"
	"path/filepath"
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
