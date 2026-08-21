// Package config 负责读取与校验 hr-compose.yml 编排文件。
package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是 hr-compose.yml 的顶层结构。
type Config struct {
	// Name 是项目名（必填），unit 文件名前缀 <name>-<service>.service，
	// 保证不同目录的同名服务互不覆盖。显式写死于此，重命名/移动目录不影响。
	Name     string              `yaml:"name"`
	Version  string              `yaml:"version"`
	Services map[string]*Service `yaml:"services"`
	// ServiceOrder 记录 services 在 yml 中的声明顺序。map 本身不保序，
	// Load 用 yaml.Node 保序读取后填入，engine 的启动/展示顺序以其为准。
	ServiceOrder []string `yaml:"-"`
}

// Service 单个服务的编排配置。字段值直接透传 systemd 指令，不做语义翻译。
type Service struct {
	Description string   `yaml:"description"`
	Command     string   `yaml:"command"`
	WorkingDir  string   `yaml:"working_dir"`
	User        string   `yaml:"user"`
	Group       string   `yaml:"group"`
	Environment []string `yaml:"environment"`
	Restart     string   `yaml:"restart"`
	RestartSec  int      `yaml:"restart_sec"`
	StopSignal  string   `yaml:"stop_signal"`
	StopTimeout int      `yaml:"stop_timeout"`
	MemoryMax   string   `yaml:"memory_max"`
	CPUQuota    string   `yaml:"cpu_quota"`
	StdOutput   string   `yaml:"std_output"`
	LogFile     string   `yaml:"log_file"`
	DependsOn   []string `yaml:"depends_on"`
}

// EffectiveDescription 返回写入 unit 的 Description 值，空值用默认占位。
func (s *Service) EffectiveDescription(name string) string {
	if s.Description == "" {
		return "hr-compose service " + name
	}
	return s.Description
}

// EffectiveRestart 返回生效的 restart 值，空值按 always（进程退出自动重启）处理。
func (s *Service) EffectiveRestart() string {
	if s.Restart == "" {
		return "always"
	}
	return s.Restart
}

// EffectiveRestartSec 返回生效的重启间隔秒数，0 按默认 5 秒处理。
func (s *Service) EffectiveRestartSec() int {
	if s.RestartSec == 0 {
		return 5
	}
	return s.RestartSec
}

// EffectiveStdOutput 返回生效的 std_output 值：
//   - 空值（未配置，或裸写 null 被 YAML 解析为空）→ 默认 null，丢弃 stdout/stderr
//   - "none" → 兼容旧写法，同样归一为 null
func (s *Service) EffectiveStdOutput() string {
	switch s.StdOutput {
	case "":
		return "null"
	case "none": // 兼容旧写法 none
		return "null"
	default:
		return s.StdOutput
	}
}

// userResolver 解析"执行 up 的真实用户"：SUDO_USER 优先（sudo 下进程是 root，取真实用户），
// 否则取系统当前用户；均不可用时兜底 root（与 systemd 未设 User 的默认一致）。
// 包级变量便于测试注入。
var userResolver = func() string {
	if sudo := os.Getenv("SUDO_USER"); sudo != "" {
		return sudo
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "root"
}

// EffectiveUser 返回生效的 User 值：显式配置原样透传，空值自动注入执行 up 的真实用户。
func (s *Service) EffectiveUser() string {
	if s.User != "" {
		return s.User
	}
	return userResolver()
}

// DefaultPath 返回默认编排文件路径（当前目录）。
func DefaultPath() string { return "hr-compose.yml" }

// Load 读取并校验编排文件。
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取编排文件: %w", err)
	}
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true) // 未知字段直接报错
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("校验 %s: %w", path, err)
	}
	if err := cfg.recordServiceOrder(string(b), path); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// recordServiceOrder 从 yml 原文解析 services 的键声明顺序，填入 ServiceOrder。
// 字段值与未知字段校验仍由结构体 Decode（KnownFields）把关，这里只取键名；
// 重复键只记录首次出现，只记录实际生效（存在于 Services map）的键。
func (c *Config) recordServiceOrder(src, path string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return fmt.Errorf("解析 %s: %w", path, err)
	}
	services := mappingValue(&doc, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil // services 缺失或非映射：Validate 已拦截，此处防御性跳过
	}
	seen := make(map[string]bool, len(c.Services))
	c.ServiceOrder = make([]string, 0, len(c.Services))
	for i := 0; i+1 < len(services.Content); i += 2 {
		key := services.Content[i].Value
		if _, ok := c.Services[key]; ok && !seen[key] {
			seen[key] = true
			c.ServiceOrder = append(c.ServiceOrder, key)
		}
	}
	return nil
}

// mappingValue 在映射节点中查找键对应的值节点；自动解开 DocumentNode 包装。
// 非映射或键不存在时返回 nil。
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	for n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// Validate 执行 schema 校验。
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("缺少必填字段 name（项目名，unit 文件名前缀，需全局唯一）")
	}
	if err := validateProjectName(c.Name); err != nil {
		return err
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("services 不能为空")
	}
	for name, svc := range c.Services {
		if err := validateServiceName(name); err != nil {
			return err
		}
		if svc == nil {
			return fmt.Errorf("服务 %q 配置为空", name)
		}
		if strings.TrimSpace(svc.Command) == "" {
			return fmt.Errorf("服务 %q 缺少必填字段 command", name)
		}
		if err := validateNoNewline(name, "command", svc.Command); err != nil {
			return err
		}
		for _, env := range svc.Environment {
			if err := validateNoNewline(name, "environment", env); err != nil {
				return err
			}
		}
		for _, dep := range svc.DependsOn {
			if _, ok := c.Services[dep]; !ok {
				return fmt.Errorf("服务 %q 的 depends_on 引用了未定义的服务 %q", name, dep)
			}
		}
	}
	return nil
}

// validateServiceName 限制服务名字符集：服务名会成为 unit 文件名。
func validateServiceName(name string) error {
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return fmt.Errorf("服务名 %q 只能包含小写字母、数字、-、_", name)
		}
	}
	return nil
}

// validateProjectName 限制项目名字符集：项目名会成为 unit 文件名前缀。
func validateProjectName(name string) error {
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return fmt.Errorf("name（项目名）%q 只能包含小写字母、数字、-、_", name)
		}
	}
	return nil
}

// defaultProjectName 返回 init 写入模板的默认项目名：yml 所在目录 basename 的 sanitize 值。
// 显式写进文件、可自行修改，重命名/移动目录不影响；sanitize 后为空时回退 my-project。
func defaultProjectName(ymlPath string) string {
	dir, err := filepath.Abs(filepath.Dir(ymlPath))
	if err != nil {
		return "my-project"
	}
	if n := sanitizeName(filepath.Base(dir)); n != "" {
		return n
	}
	return "my-project"
}

// sanitizeName 把任意目录名规范化为合法项目名：小写、非法字符转 -、合并连续 -、去首尾 -。
func sanitizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// validateNoNewline 校验写入 unit 指令的值不含换行，防止向 systemd unit 文件注入额外指令。
func validateNoNewline(name, field, v string) error {
	if strings.ContainsAny(v, "\n\r") {
		return fmt.Errorf("服务 %q 的 %s 不能包含换行符", name, field)
	}
	return nil
}
