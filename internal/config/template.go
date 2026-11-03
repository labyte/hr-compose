package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// templateHeader 是所有 init 生成文件共享的字段说明注释：默认模板与目录扫描生成的内容
// 都带上同一段字段参考，便于用户后续手补 depends_on / std_output 等未在初始配置里展开的字段。
const templateHeader = `# hr-compose.yml
# 由 hr-compose init 生成，请按需修改后再使用 hr-compose up。
#
# 字段说明（取值直接透传 systemd 指令，语义以 systemd 为准）：
#   name          项目名，必填。unit 文件名前缀（<name>-<service>.service），需全局唯一；
#                 显式写死于此，重命名/移动本目录不影响。请改为你的项目名。
#   description   服务描述，写入 unit 的 Description（默认 "hr-compose service <name>"）
#   command       必填。启动命令，必须前台运行，不要 daemon
#   working_dir   工作目录
#   user / group  运行身份 / 运行组（user 省略时自动注入执行 up 的真实用户）
#   environment   环境变量，每行一条 "KEY=VALUE"
#   restart       no / on-success / on-failure / on-abnormal / on-abort / on-watchdog / always（默认）
#   restart_sec   重启间隔（秒，默认 5）
#   stop_signal   停止信号：SIGTERM（默认）/ SIGKILL / SIGINT / SIGHUP 等
#   stop_timeout  停止宽限期（秒，默认 90）
#   memory_max    内存上限，大小带单位：2G / 500M / 1024K
#   cpu_quota     CPU 配额，百分比：100% = 1 核 / 200% = 2 核
#   std_output    null（默认，丢弃输出；裸写 null 即可）/ none（兼容旧写法）/ journal / file:<path> / append:<path>
#   log_file      std_output 为 null 时的业务日志路径：logs 命令直接 tail 该文件
#                 （支持 glob，如 /var/log/app/*.log，查看时取 mtime 最新者，适配按日期切文件的业务库）
#   depends_on    启动顺序依赖（软依赖 Wants=，仅控制顺序、依赖失败不阻塞）；
#                  depends_on_required: true 时改为强依赖 Requires=（依赖启动失败则本服务不启动，
#                  且依赖被停止/失败会联动停掉本服务）
#   depends_on_required  本服务 depends_on 是否强依赖（默认 false，需配合 depends_on 使用）
#   start_delay   启动前延时秒数，默认 0 不延时；在依赖启动完成后才开始计时（计入启动超时，见 start_timeout）
#   start_timeout 启动超时秒数，默认 90（与 systemd 默认一致）；start_delay 较大时需同步放宽，如 start_delay: 120 配 start_timeout: 180`

// defaultTemplate 返回 init 命令生成的默认编排文件模板，project 为写入的默认项目名。
func defaultTemplate(project string) string {
	return templateHeader + `

version: "1.0"
name: ` + project + `

services:
  # 以下 api / web 两个服务为示例：演示最小配置与 depends_on 依赖写法，请按需修改或删除。
  api:
    description: 示例 API 服务
    command: /opt/myapp/api
    working_dir: /opt/myapp

  web:
    description: 示例 Web 服务
    command: /opt/myapp/web
    working_dir: /opt/myapp
    depends_on:
      - api        # web 依赖 api：up 时先启动 api，再启动 web
`
}

// Init 生成默认编排文件模板；文件已存在则不覆盖。
func Init(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s 已存在，不覆盖（如需重建请先删除）", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(defaultTemplate(defaultProjectName(path))), 0o644)
}

// InitFromDir 扫描 dir 下的直接子文件夹，每个子文件夹作为一个服务生成初始编排文件
// （只写 command 与 working_dir，不生成 depends_on 等关系；command 探测不到时用占位并加 TODO）。
// 文件已存在则不覆盖（同 Init）。生成内容复用 templateHeader 的字段说明注释。
func InitFromDir(dir, path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s 已存在，不覆盖（如需重建请先删除）", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	content, err := scanTemplate(dir, defaultProjectName(path))
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// scannedService 是扫描出的一个服务：sanitize 后的服务名、子文件夹绝对路径、启动命令及是否探测到。
type scannedService struct {
	name     string
	dirPath  string
	command  string
	detected bool
}

// scanTemplate 扫描 dir 下的直接子文件夹生成编排文件内容，project 为写入的项目名。
// 只认直接子目录（跳过文件、隐藏项与符号链接），服务名按子文件夹名 sanitize 后排序保证输出稳定。
func scanTemplate(dir, project string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("解析服务目录 %s: %w", dir, err)
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return "", fmt.Errorf("读取服务目录 %s: %w", absDir, err)
	}

	svcs := make([]scannedService, 0, len(entries))
	seen := make(map[string]bool)
	for _, ent := range entries {
		// 只扫直接子目录：IsDir 对符号链接返回 false（按链接自身类型），普通文件、隐藏项一并跳过。
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		name := sanitizeName(ent.Name())
		if name == "" {
			continue // 目录名全为非法字符、sanitize 后为空，无法作服务名，跳过
		}
		if seen[name] {
			return "", fmt.Errorf("服务名冲突：%q 与已有服务规范化后同名 %q", ent.Name(), name)
		}
		seen[name] = true
		sub := filepath.Join(absDir, ent.Name())
		cmd, detected := detectCommand(sub, name)
		svcs = append(svcs, scannedService{name: name, dirPath: sub, command: cmd, detected: detected})
	}
	if len(svcs) == 0 {
		return "", fmt.Errorf("%s 下未发现任何服务子目录", absDir)
	}
	sort.Slice(svcs, func(i, j int) bool { return svcs[i].name < svcs[j].name })

	var b strings.Builder
	b.WriteString(templateHeader)
	b.WriteString("\n\nversion: \"1.0\"\nname: ")
	b.WriteString(project)
	b.WriteString("\n\nservices:\n")
	for i, s := range svcs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  ")
		b.WriteString(s.name)
		b.WriteString(":\n")
		if !s.detected {
			b.WriteString("    # TODO: 未探测到启动命令，请改成实际启动命令\n")
		}
		b.WriteString("    command: ")
		b.WriteString(yamlScalar(s.command))
		b.WriteString("\n    working_dir: ")
		b.WriteString(yamlScalar(s.dirPath))
		b.WriteString("\n")
	}
	return b.String(), nil
}

// detectCommand 探测子文件夹的启动命令：优先同名可执行文件、bin/<name>，其次子目录里唯一带
// 可执行位的文件；都找不到时回退占位 ./<name>（detected=false，调用方补 TODO 注释提示用户修改）。
func detectCommand(dir, name string) (string, bool) {
	if p := filepath.Join(dir, name); isExecutable(p) {
		return p, true
	}
	if p := filepath.Join(dir, "bin", name); isExecutable(p) {
		return p, true
	}
	if p := onlyExecutable(dir); p != "" {
		return p, true
	}
	return "./" + name, false
}

// isExecutable 判断路径是否为带可执行位的常规文件。
func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode()&0o111 != 0
}

// onlyExecutable 返回 dir 下唯一的可执行常规文件；多于一个或无则返回空（不猜）。
func onlyExecutable(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var found string
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		fi, err := ent.Info()
		if err != nil || fi.Mode()&0o111 == 0 {
			continue
		}
		if found != "" {
			return "" // 多于一个可执行文件，不猜
		}
		found = filepath.Join(dir, ent.Name())
	}
	return found
}

// yamlScalar 把字符串渲染成 YAML 标量：常规字符（字母数字/_ . / -）原样输出，其余用双引号包裹
// （strconv.Quote 的转义 YAML 双引号串可接受），避免路径含空格等特殊字符时生成非法 yml。
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '_' || r == '.' || r == '/' || r == '-' {
			continue
		}
		return strconv.Quote(s)
	}
	return s
}
