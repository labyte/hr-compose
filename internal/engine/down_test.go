package engine

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hr.compose/internal/config"
)

// downFake 用于验证 down 的状态分流与失败上报：按 unit 指定状态、让 Stop/Disable 失败、记录动作。
type downFake struct {
	fakeSys
	state       map[string]string
	stopFail    bool
	disableFail bool
	blank       bool // 模拟 systemctl 未返回该 unit 任何字段（状态未知）
}

func (f *downFake) show() map[string]string {
	if f.blank {
		return map[string]string{}
	}
	s := fakeShowFields()
	for k, v := range f.state {
		s[k] = v
	}
	return s
}

func (f *downFake) Show(u string) (map[string]string, error) { return f.show(), nil }

func (f *downFake) ShowMany(units []string) (map[string]map[string]string, error) {
	m := make(map[string]map[string]string, len(units))
	for _, u := range units {
		m[u] = f.show()
	}
	return m, nil
}

func (f *downFake) Stop(u string) error {
	f.actions = append(f.actions, "stop "+u)
	if f.stopFail {
		return errors.New("stop 失败 " + u)
	}
	return nil
}

func (f *downFake) Disable(u string) error {
	f.actions = append(f.actions, "disable "+u)
	if f.disableFail {
		return errors.New("disable 失败 " + u)
	}
	return nil
}

// downSetup 准备 down 测试环境：临时 UnitDir、捕获 stdout，并写入一个托管 unit 文件（write 为 false 时跳过）。
func downSetup(t *testing.T, write bool) (*Engine, *downFake, *bytes.Buffer) {
	t.Helper()
	oldDir := UnitDir
	UnitDir = t.TempDir()
	t.Cleanup(func() { UnitDir = oldDir })
	oldOut := stdout
	buf := &bytes.Buffer{}
	stdout = buf
	t.Cleanup(func() { stdout = oldOut })
	if write {
		p := filepath.Join(UnitDir, "api.service")
		if err := os.WriteFile(p, []byte("# MANAGED BY hr-compose\n[Service]\nExecStart=/bin/true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sys := &downFake{}
	cfg := &config.Config{Services: map[string]*config.Service{
		"api": {Command: "/x/api"},
	}}
	return New(cfg, sys), sys, buf
}

func TestDownDoesNotDeleteWhenStopFails(t *testing.T) {
	e, sys, buf := downSetup(t, true)
	sys.state = map[string]string{"ActiveState": "active", "LoadState": "loaded"}
	sys.stopFail = true

	if err := e.Down(); err == nil {
		t.Fatal("stop 失败时 down 应返回错误")
	}
	if _, err := os.Stat(filepath.Join(UnitDir, "api.service")); err != nil {
		t.Errorf("stop 失败时不应删除 unit 文件: %v", err)
	}
	if strings.Contains(buf.String(), "down api OK") {
		t.Errorf("stop 失败时不应打印 down api OK: %q", buf.String())
	}
}

func TestDownDoesNotDeleteWhenDisableFails(t *testing.T) {
	e, sys, buf := downSetup(t, true)
	sys.state = map[string]string{"ActiveState": "inactive", "LoadState": "loaded"}
	sys.disableFail = true

	if err := e.Down(); err == nil {
		t.Fatal("disable 失败时 down 应返回错误")
	}
	if _, err := os.Stat(filepath.Join(UnitDir, "api.service")); err != nil {
		t.Errorf("disable 失败时不应删除 unit 文件: %v", err)
	}
	if strings.Contains(buf.String(), "down api OK") {
		t.Errorf("disable 失败时不应打印 down api OK: %q", buf.String())
	}
}

func TestDownSkipsStopWhenInactive(t *testing.T) {
	e, sys, _ := downSetup(t, true)
	sys.state = map[string]string{"ActiveState": "inactive", "LoadState": "loaded"}

	if err := e.Down(); err != nil {
		t.Fatal(err)
	}
	if containsAction(sys.actions, "stop api.service") {
		t.Errorf("已停止的服务不应再 stop: %v", sys.actions)
	}
	if _, err := os.Stat(filepath.Join(UnitDir, "api.service")); !os.IsNotExist(err) {
		t.Errorf("已停止的服务仍应删除 unit 文件: %v", err)
	}
}

func TestDownNotInstalledIsNoop(t *testing.T) {
	// 未安装（LoadState=not-found）：不 stop、不 disable、不报错，down 幂等。
	e, sys, _ := downSetup(t, false)
	sys.state = map[string]string{"ActiveState": "inactive", "LoadState": "not-found"}

	if err := e.Down(); err != nil {
		t.Fatal(err)
	}
	if containsAction(sys.actions, "stop api.service") || containsAction(sys.actions, "disable api.service") {
		t.Errorf("未安装的服务不应 stop/disable: %v", sys.actions)
	}
}

func TestDownSkipsDeleteWhenStateUnknown(t *testing.T) {
	// 状态查不到（systemctl 未返回字段）：连在不在跑都无法确定，宁可报错也不删文件留孤儿。
	e, sys, buf := downSetup(t, true)
	sys.blank = true

	if err := e.Down(); err == nil {
		t.Fatal("状态未知时 down 应返回错误")
	}
	if _, err := os.Stat(filepath.Join(UnitDir, "api.service")); err != nil {
		t.Errorf("状态未知时不应删除 unit 文件: %v", err)
	}
	if strings.Contains(buf.String(), "down api OK") {
		t.Errorf("状态未知时不应打印 down api OK: %q", buf.String())
	}
}

func TestDownRefusesForeignUnitBeforeStopDisable(t *testing.T) {
	// 同名 unit 文件存在但非 hr-compose 托管（如系统同名服务）时，托管保护必须连同 stop/disable 一起拒绝，
	// 不能先停掉外来服务的进程再去删文件——此前只挡删除，stop/disable 会先动到它。
	e, sys, buf := downSetup(t, false)
	// 覆盖为无托管标记的同名外来文件。
	p := filepath.Join(UnitDir, "api.service")
	if err := os.WriteFile(p, []byte("[Service]\nExecStart=/bin/false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sys.state = map[string]string{"ActiveState": "active", "LoadState": "loaded"}

	if err := e.Down(); err == nil {
		t.Fatal("非托管同名 unit 时 down 应返回错误")
	}
	if containsAction(sys.actions, "stop api.service") || containsAction(sys.actions, "disable api.service") {
		t.Errorf("非托管同名 unit 不应 stop/disable: %v", sys.actions)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("非托管同名 unit 文件不应被删除: %v", err)
	}
	if strings.Contains(buf.String(), "down api OK") {
		t.Errorf("非托管同名 unit 不应打印 down api OK: %q", buf.String())
	}
}
