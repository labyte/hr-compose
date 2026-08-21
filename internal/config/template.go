package config

import (
	"fmt"
	"os"
)

// defaultTemplate 返回 init 命令生成的默认编排文件模板，project 为写入的默认项目名。
func defaultTemplate(project string) string {
	return `# hr-compose.yml
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
#   log_file      std_output 为 null 时的外部日志路径（仅 logs 提示用）
#   depends_on    启动顺序依赖（仅控制顺序，不阻塞失败）

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
