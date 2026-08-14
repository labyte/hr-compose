// Package config 负责读取与校验 hr-compose.yml 编排文件。
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是 hr-compose.yml 的顶层结构。
type Config struct {
	Version  string              `yaml:"version"`
	Services map[string]*Service `yaml:"services"`
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

// EffectiveStdOutput 返回生效的 std_output 值，空值按 journal 处理。
func (s *Service) EffectiveStdOutput() string {
	if s.StdOutput == "" {
		return "journal"
	}
	return s.StdOutput
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
	return &cfg, nil
}

// Validate 执行 schema 校验。
func (c *Config) Validate() error {
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
