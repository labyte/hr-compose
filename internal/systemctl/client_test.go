package systemctl

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeSystemctl 在 PATH 最前面放一个假的 systemctl 脚本，按给定 stdout/stderr/退出码响应，
// 使 Real 的调用路径在没有 systemd 的环境（CI 容器、非 systemd 主机）里同样可测。
func fakeSystemctl(t *testing.T, stdout, stderr string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	if strings.ContainsAny(dir, "'\" \t\\") {
		t.Skipf("临时目录含 shell 特殊字符，跳过：%s", dir)
	}
	for name, body := range map[string]string{"out": stdout, "err": stderr} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\n" +
		"cat '" + filepath.Join(dir, "out") + "'\n" +
		"cat '" + filepath.Join(dir, "err") + "' >&2\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestExecErrCarriesStderr(t *testing.T) {
	// 用真实失败命令构造带 Stderr 的 *exec.ExitError：Output() 会把 stderr 收进 ExitError.Stderr，
	// 与 Show/ShowMany 的调用方式一致，不依赖 systemctl 是否存在。
	_, err := exec.Command("sh", "-c", "printf '%s\\n' 'Failed to connect to bus: Host is down' >&2; exit 3").Output()
	if err == nil {
		t.Fatal("期望命令失败以构造 ExitError")
	}
	got := execErr("systemctl", []string{"show", "api.service"}, err)
	msg := got.Error()
	for _, want := range []string{"systemctl show api.service", "exit status 3", "Failed to connect to bus"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息应含 %q，got:\n%s", want, msg)
		}
	}
	if strings.HasSuffix(msg, "\n") {
		t.Errorf("stderr 末尾换行应被裁掉，got %q", msg)
	}
	// 错误链保留：上层仍可用 errors.As 判定是不是"命令执行失败"
	var exitErr *exec.ExitError
	if !errors.As(got, &exitErr) {
		t.Error("包装后应仍能 errors.As 出 *exec.ExitError")
	}
}

func TestExecErrWithoutStderr(t *testing.T) {
	// 回归点：包装前 Show/ShowMany 直接返回 exec 的原始错误，上层只剩 "exit status 1"，
	// 用户看不出是连不上 systemd、权限不足还是 unit 不存在。
	got := execErr("systemctl", []string{"show", "api.service"}, errors.New("exit status 1"))
	if want := "systemctl show api.service: exit status 1"; got.Error() != want {
		t.Errorf("got %q, want %q", got.Error(), want)
	}
	// 无参数时只带命令名
	if got := execErr("systemctl", nil, errors.New("boom")); got.Error() != "systemctl: boom" {
		t.Errorf("got %q, want %q", got.Error(), "systemctl: boom")
	}
}

func TestExecErrExitWithoutStderr(t *testing.T) {
	_, err := exec.Command("sh", "-c", "exit 3").Output()
	if err == nil {
		t.Fatal("期望命令失败")
	}
	got := execErr("systemctl", []string{"show", "x.service"}, err)
	if want := "systemctl show x.service: exit status 3"; got.Error() != want {
		t.Errorf("got %q, want %q", got.Error(), want)
	}
}

func TestShowSurfacesStderr(t *testing.T) {
	fakeSystemctl(t, "", "Failed to connect to bus: Host is down\n", 1)
	_, err := (&Real{}).Show("api.service")
	if err == nil {
		t.Fatal("systemctl 失败时 Show 应返回错误")
	}
	msg := err.Error()
	for _, want := range []string{"systemctl show api.service", "exit status 1", "Failed to connect to bus"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Show 的错误应带命令与 systemd 的 stderr，缺 %q，got:\n%s", want, msg)
		}
	}
}

func TestShowManySurfacesStderrWithoutPropList(t *testing.T) {
	fakeSystemctl(t, "", "Unit nope.service could not be found.\n", 4)
	_, err := (&Real{}).ShowMany([]string{"api.service", "nope.service"})
	if err == nil {
		t.Fatal("systemctl 失败且无任何输出时 ShowMany 应返回错误")
	}
	msg := err.Error()
	for _, want := range []string{"systemctl show api.service nope.service", "could not be found"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息应含 %q，got:\n%s", want, msg)
		}
	}
	// -p 属性列表是实现细节，塞进错误只会淹没真正的原因
	if strings.Contains(msg, "-p ") {
		t.Errorf("错误信息不该带 -p 属性列表:\n%s", msg)
	}
}

func TestShowParsesFields(t *testing.T) {
	fakeSystemctl(t, "Id=api.service\nActiveState=active\nSubState=running\nMemoryCurrent=[not set]\n", "", 0)
	fields, err := (&Real{}).Show("api.service")
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"ActiveState": "active", "SubState": "running", "MemoryCurrent": "[not set]",
	} {
		if fields[k] != want {
			t.Errorf("Show[%q] = %q, want %q", k, fields[k], want)
		}
	}
}

func TestShowManyParsesMultipleBlocks(t *testing.T) {
	// 多 unit 时 systemctl 用空行分块，按 Id 归组——ps/up 复核/start-vs-restart 决策都依赖这个解析
	out := "Id=a.service\nActiveState=active\nSubState=running\nLoadState=loaded\n\n" +
		"Id=b.service\nActiveState=inactive\nSubState=dead\nLoadState=not-found\n"
	fakeSystemctl(t, out, "", 0)
	got, err := (&Real{}).ShowMany([]string{"a.service", "b.service"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("应解析出 2 个 unit，got %v", got)
	}
	if got["a.service"]["SubState"] != "running" || got["a.service"]["LoadState"] != "loaded" {
		t.Errorf("a.service 字段不符: %v", got["a.service"])
	}
	if got["b.service"]["LoadState"] != "not-found" || got["b.service"]["ActiveState"] != "inactive" {
		t.Errorf("b.service 字段不符: %v", got["b.service"])
	}
}

func TestShowManyKeepsPartialResultOnNonZeroExit(t *testing.T) {
	// 部分 unit 未加载时 systemctl 退出码非 0 但仍输出其余 unit 的块：解析可用部分，不报错
	fakeSystemctl(t, "Id=a.service\nActiveState=active\n", "No such unit b.service\n", 1)
	got, err := (&Real{}).ShowMany([]string{"a.service", "b.service"})
	if err != nil {
		t.Errorf("已解析到部分结果时不应报错: %v", err)
	}
	if got["a.service"]["ActiveState"] != "active" {
		t.Errorf("应保留可用的 a.service 状态: %v", got)
	}
	if _, ok := got["b.service"]; ok {
		t.Errorf("无 Id 的空块不应进入结果: %v", got)
	}
}

func TestShowManyEmptyUnitsSkipsCall(t *testing.T) {
	// 空列表必须短路：`systemctl show` 不带 unit 会打印系统上全部 unit
	fakeSystemctl(t, "Id=should-not-appear.service\n", "", 0)
	got, err := (&Real{}).ShowMany(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("空输入应返回空结果，got %v", got)
	}
}
