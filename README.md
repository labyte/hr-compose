# hr-compose

> 对标 docker-compose 的本地服务编排工具（CLI）：读一份 `hr-compose.yml`，基于 systemd 在 Linux 裸机上管理多个业务服务的启停与编排。

```bash
hr-compose up / down / restart / ps / logs / config
```

- **复用 systemd**：进程监控、自启、重启、日志全部交给 systemd，工具本身是单一二进制、无外部依赖
- **裸机优先**：生成 systemd unit 文件，无容器
- **yml 即事实来源**：只管理当前目录 `hr-compose.yml` 中定义的服务

---

## 安装

### 在线安装（推荐）

需要 `curl`，默认安装到 `/usr/local/bin`：

```bash
curl -fsSL https://github.com/labyte/hr-compose/releases/latest/download/install.sh | sh
```

常用变体：

```bash
# 安装指定版本
HR_COMPOSE_VERSION=v1.0.0 curl -fsSL https://github.com/labyte/hr-compose/releases/latest/download/install.sh | sh

# 安装到其他目录（无需 root）
HR_COMPOSE_INSTALL_DIR=$HOME/.local/bin curl -fsSL https://github.com/labyte/hr-compose/releases/latest/download/install.sh | sh
```

> 脚本会从 GitHub Releases 下载对应平台（linux / darwin × amd64 / arm64）的发行包，并用官方 `checksums.txt` 校验 SHA256 后再安装。

### 从源码构建

```bash
make build        # 产物在 bin/hr-compose
make cross        # 交叉编译 linux/amd64 + linux/arm64 + darwin/arm64
```

### 离线安装（下载发行包）

无法用在线脚本（无外网 / 受限环境）时，把发行包直接拷到目标服务器安装。

先在任意有网的机器上下载对应平台的压缩包与校验文件（版本号换成实际 tag，如 `v1.0.0`）：

```bash
# x86_64 服务器选 amd64，ARM64 服务器选 arm64
ASSET=hr-compose_1.0.0_linux_amd64.tar.gz
curl -fsSLO https://github.com/labyte/hr-compose/releases/download/v1.0.0/$ASSET
curl -fsSLO https://github.com/labyte/hr-compose/releases/download/v1.0.0/checksums.txt
```

把这两个文件拷到目标服务器（U 盘 / `scp` / 内网盘），在服务器上校验并安装：

```bash
cd /tmp
grep "$ASSET" checksums.txt | sha256sum -c -   # 校验 SHA256，输出 OK 即通过
tar -xzf "$ASSET"
sudo install -m 0755 hr-compose /usr/local/bin/
hr-compose --help
```

> 平台对应：`uname -m` 输出 `x86_64` → `amd64`，`aarch64` / `arm64` → `arm64`；macOS 本地用 `hr-compose_1.0.0_darwin_arm64.tar.gz`。也可在 [Releases 页面](https://github.com/labyte/hr-compose/releases) 直接下载对应平台压缩包。

---

## 快速开始

在项目目录写一份 `hr-compose.yml`：

```yaml
services:
  redis:
    command: /opt/redis/redis-server /opt/redis/redis.conf
    user: appuser
    restart: always
    std_output: "null"                 # 丢弃 stdout/stderr，redis 自己写日志
    log_file: /var/log/redis/redis.log
  api:
    command: /opt/myapp/api            # 必须前台运行，不要 daemon
    working_dir: /opt/myapp
    user: appuser
    environment:
      - "DB_ADDR=127.0.0.1:3306"
    depends_on:                        # 启动顺序：先 redis 后 api
      - redis
```

然后（操作 `/etc/systemd/system`，`up`/`down` 需要 root）：

```bash
sudo hr-compose up          # 生成 unit、enable + start，按依赖顺序启动
hr-compose ps               # 查看服务状态
hr-compose logs api         # 查看日志（journal 模式走 journalctl）
hr-compose logs redis       # 提示 tail 查看 redis 自己的日志文件
sudo hr-compose restart api # 重启某个服务
sudo hr-compose down        # 停止并清理（删除 unit 文件）
```

---

## 命令参考

| 命令 | 说明 | 示例 |
| --- | --- | --- |
| `up` | 生成 unit 并启动全部服务，遵循 `depends_on` 顺序；重复执行幂等 | `sudo hr-compose up` |
| `down` | 停止并清理全部服务，删除 unit 文件 | `sudo hr-compose down` |
| `restart [name]` | 重启指定服务，不指定则全部 | `sudo hr-compose restart api` |
| `ps` | 列出 yml 中各服务状态（ACTIVE / SUB / PID / MEMORY） | `hr-compose ps` |
| `logs [name] [-f]` | 查看日志；`-f` 实时跟踪，按 `std_output` 分发 | `hr-compose logs api -f` |
| `config` | 校验 yml 并打印生成的 unit 内容（调试用） | `hr-compose config` |

全局参数：`--file <path>` 指定编排文件（默认当前目录 `hr-compose.yml`）。注意 `-f` 是 `logs` 的 `--follow` 简写。

---

## 编排文件参考

默认读取当前目录的 `hr-compose.yml`。

### 完整示例

```yaml
version: "1.0"
services:
  api:
    command: /opt/myapp/api            # ExecStart，必须前台运行，不要 daemon
    working_dir: /opt/myapp            # WorkingDirectory
    user: appuser                      # User
    group: appuser                     # Group
    environment:                       # Environment=，每行一条
      - "DB_ADDR=127.0.0.1:3306"
      - "LOG_LEVEL=info"
    restart: on-failure                # Restart
    restart_sec: 5                     # RestartSec
    stop_signal: SIGTERM               # KillSignal
    stop_timeout: 30                   # TimeoutStopSec，优雅停止宽限期（秒）
    memory_max: 2G                     # MemoryMax
    cpu_quota: 200%                    # CPUQuota
    std_output: journal                # StandardOutput/StandardError
    depends_on:                        # After= + Wants=，仅控制启动顺序
      - redis
  redis:
    command: /opt/redis/redis-server /opt/redis/redis.conf
    working_dir: /opt/redis
    user: appuser
    restart: always
    std_output: "null"                 # 丢弃 stdout/stderr（null 需加引号）
    log_file: /var/log/redis/redis.log # 仅用于 logs 命令的 tail 提示
```

### 字段说明

> 所有字段值直接透传给对应 systemd 指令，**不做 compose 语义翻译**，取值以 systemd 为准。

| 字段 | 说明 | 写入的 systemd 指令 |
| --- | --- | --- |
| `command` | 启动命令（**必须前台运行，不要 daemon**，必填） | `ExecStart` |
| `working_dir` | 工作目录 | `WorkingDirectory` |
| `user` / `group` | 运行身份 / 运行组 | `User` / `Group` |
| `environment` | 环境变量，每行一条 | `Environment=` |
| `restart` | 重启策略（no / on-success / on-failure / always 等） | `Restart` |
| `restart_sec` | 重启间隔秒数 | `RestartSec` |
| `stop_signal` | 停止信号（默认 SIGTERM） | `KillSignal` |
| `stop_timeout` | 停止宽限期秒数（默认 90） | `TimeoutStopSec` |
| `memory_max` | 内存上限 | `MemoryMax` |
| `cpu_quota` | CPU 配额 | `CPUQuota` |
| `std_output` | 日志目标（journal / `"null"` / file:`<path>` / append:`<path>`） | `StandardOutput=`、`StandardError=` |
| `log_file` | 外部日志文件路径，仅用于 `logs` 提示 | —（不参与 systemd 配置） |
| `depends_on` | 依赖的服务，仅控制启动顺序 | `After=` + `Wants=` |

### 日志目标（std_output）

| 取值 | 含义 | `hr-compose logs` 行为 |
| --- | --- | --- |
| `journal`（默认） | 写入 journald | 执行 `journalctl -u <svcname>.service [-f]` |
| `"null"` | 丢弃 stdout/stderr，日志由业务程序自行写入外部文件。**YAML 里必须加引号** | 提示用 `tail -f` 查看外部日志文件（配合 `log_file`） |
| `file:<path>` | 覆盖写入（截断原文件） | 提示用 `tail -f <path>` 查看 |
| `append:<path>` | 追加写入 | 提示用 `tail -f <path>` 查看 |

> `file:` / `append:` 的文件由 systemd 以 `User=` 身份打开，需保证目录对该用户可写；`file:` 会截断已有文件。

---

## 注意事项

- **需要 root**：`up` / `down` / `restart` 操作 systemd（`/etc/systemd/system`），需 `sudo`。`ps` / `logs` / `config` 为只读，普通用户即可。
- **服务名即 unit 文件名**：服务名只用小写字母、数字、`-`、`_`；避免与系统已有 unit 同名冲突。
- **删除保护**：生成的 unit 文件头部有 `# MANAGED BY hr-compose` 标记，`down` 只删除带该标记的文件，防止误删同名系统服务。
- **`std_output: null` 必须加引号**：不加引号是 YAML 的 `null` 字面量，等价于"未配置"，会被当作默认 `journal`。
- **`command` 必须前台运行**：值直接作为 `ExecStart`，业务程序不能 daemonize，否则 systemd 会认为服务未启动。
- **无 project 概念**：工具只管理当前目录 `hr-compose.yml` 中定义的服务，不扫描系统上其他 unit。

---

## 问题排查

```bash
hr-compose config            # 校验 yml、预览生成的 unit 内容
systemctl status <svc>.service   # 查看 systemd 层面的真实状态
journalctl -u <svc>.service      # 查看 journal 日志
```
