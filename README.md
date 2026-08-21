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

> 完整离线流程（含校验、安装、使用、更新卸载）见 [docs/offline-install.md](docs/offline-install.md)。

### 更新

重新执行在线安装命令即可升级到最新版（脚本会下载最新发行包并覆盖旧二进制）：

```bash
curl -fsSL https://github.com/labyte/hr-compose/releases/latest/download/install.sh | sh
```

- **指定版本更新**：`HR_COMPOSE_VERSION=v1.0.0 curl -fsSL ... | sh`
- **离线更新**：按上面离线安装的方式，重新下载新版本 tarball 覆盖旧二进制即可
- 更新工具**不会**改动已有的 systemd unit 和运行中的服务；若新版生成的 unit 内容有变，重新执行一次 `sudo hr-compose up`（幂等）即可让配置生效

### 卸载

```bash
sudo hr-compose down          # 1. 停止并清理所有托管服务（只删带托管标记的 unit）
sudo rm /usr/local/bin/hr-compose   # 2. 删除二进制（若装了自定义目录，删对应文件）
```

> `hr-compose down` 只删除由 hr-compose 生成（带 `# MANAGED BY hr-compose` 标记）的 unit 文件，不会误删同名系统服务。若服务名与系统已有 unit 重名，`down` 会拒绝删除并报错。

---

## 快速开始

在项目目录写一份 `hr-compose.yml`（也可以先 `hr-compose init` 生成模板再编辑）：

```yaml
services:
  redis:
    command: /opt/redis/redis-server /opt/redis/redis.conf
    user: appuser
    restart: always
    std_output: null                   # 丢弃 stdout/stderr（裸写 null 即可），redis 自己写日志
    log_file: /var/log/redis/redis.log
  api:
    command: /opt/myapp/api            # 必须前台运行，不要 daemon
    working_dir: /opt/myapp
    user: appuser
    environment:
      - "DB_ADDR=127.0.0.1:3306"
    # std_output 未配置，默认 null（丢弃输出，日志由业务程序自管）；要 journald 需显式 std_output: journal
    depends_on:                        # 启动顺序：先 redis 后 api
      - redis
```

然后（操作 `/etc/systemd/system`，`up`/`down` 需要 root）：

```bash
sudo hr-compose up          # 生成 unit、enable + start，按依赖顺序启动
hr-compose ps               # 查看服务状态
hr-compose logs api         # 查看日志（api 未配 std_output，默认 null → 提示 tail 业务日志）
hr-compose logs redis       # 提示 tail 查看 redis 自己的日志文件
sudo hr-compose restart api # 重启某个服务
sudo hr-compose down        # 停止并清理（删除 unit 文件）
```

---

## 命令参考

| 命令 | 说明 | 示例 |
| --- | --- | --- |
| `init` | 生成默认 `hr-compose.yml` 模板（已存在则不覆盖） | `hr-compose init` |
| `up` | 生成 unit 并启动全部服务，遵循 `depends_on` 顺序；重复执行幂等，完成后自动展示各服务状态 | `sudo hr-compose up` |
| `start [name]` | 启动已安装的服务（不重生成 unit），不指定则全部；需先 `up` 过 | `sudo hr-compose start` |
| `stop [name]` | 停止服务（保留 unit 与 enable，不删除），不指定则全部 | `sudo hr-compose stop api` |
| `down` | 停止并清理全部服务，删除 unit 文件；若有 journal 日志服务则清空系统 journal | `sudo hr-compose down` |
| `clean [name]` | 清除服务日志（journal 清空 / file 截断），不指定则全部 | `sudo hr-compose clean api` |
| `restart [name]` | 重启指定服务，不指定则全部 | `sudo hr-compose restart api` |
| `enable [name]` | 设置服务开机启动（仅 enable，不启停），不指定则全部 | `sudo hr-compose enable api` |
| `disable [name]` | 取消服务开机启动（仅 disable，不删 unit），不指定则全部 | `sudo hr-compose disable api` |
| `ps` | 带边框列出服务状态表（STATUS 合并状态 / ENABLED 开机启动 / UPTIME 运行时长 / CONFIG 配置文件 / DESCRIPTION 描述列），内存自动转友好单位，终端下状态彩色显示 | `hr-compose ps` |
| `logs [name] [-f]` | 查看日志；`-f` 实时跟踪，按 `std_output` 分发 | `hr-compose logs api -f` |
| `config [name]` | 校验 yml 并打印生成的 unit 内容，可指定单个服务 | `hr-compose config api` |

全局参数：`--file <path>` 指定编排文件（默认当前目录 `hr-compose.yml`）。注意 `-f` 是 `logs` 的 `--follow` 简写。

### ps 状态列说明

`hr-compose ps` 的状态列来自 systemd：

| 列 | 对应 systemd | 含义 |
| --- | --- | --- |
| **STATUS** | `ActiveState` + `SubState` | 两级状态合并：`active` 运行 / `inactive` 未运行 / `failed` 失败 / `restarting` 自动重启中 / `activating/start` 启动中 / `deactivating` 停止中 / `reloading` 重载中；子状态与主状态重合或为默认值时省略（如 `active` 的 `running`、`inactive` 的 `dead`），特殊组合保留 `主/子` 完整展示 |
| **ENABLED** | `UnitFileState` | 是否开机启动：`enabled` 已启用 / `disabled` 未启用 / `static` 等 |
| **UPTIME** | `ExecMainStartTimestampMonotonic` | 主进程已运行时长（`45s` / `5m` / `2h` / `3d`）；服务重启后从头累计，可据此判断是否重启过；停止/失败/未启动的服务显示 `-` |
| **CONFIG** | `FragmentPath` | systemd 实际加载的 unit 文件路径（如 `/etc/systemd/system/<服务名>.service`） |

MEMORY 列为 systemd 报告的内存字节数，自动格式化为 `K / M / G` 友好单位（未统计时显示 `-`）。

开机启动状态用 `enable` / `disable` 命令修改（`up` 会自动 enable，`down` 会 disable）。

---

## 编排文件参考

默认读取当前目录的 `hr-compose.yml`。

### 最小配置示例

只写 描述 / 启动命令 / 工作目录 三个字段即可，其余走代码默认值：

```yaml
services:
  app:
    description: 应用服务              # Description，服务描述
    command: /opt/myapp/app          # ExecStart，启动命令（必填，必须前台运行）
    working_dir: /opt/myapp          # WorkingDirectory，工作目录
```

未配置字段的默认行为：

| 未配置字段 | 默认行为 |
| --- | --- |
| `user` | 以执行 `up` 的真实用户运行（sudo 下取 `SUDO_USER`） |
| `restart` | `always`，进程退出自动重启 |
| `restart_sec` | `5`，重启间隔 5 秒 |
| `std_output` | `null`，丢弃 stdout/stderr，日志由业务程序自行管理 |

需要指定用户、调整重启策略或让日志进 journald 时，再显式配置对应字段（完整字段见下）。

### 完整示例

```yaml
version: "1.0"
services:
  api:
    description: 主业务 API 服务          # Description，服务描述
    command: /opt/myapp/api            # ExecStart，必须前台运行，不要 daemon
    working_dir: /opt/myapp            # WorkingDirectory
    user: appuser                      # User
    group: appuser                     # Group
    environment:                       # Environment=，每行一条
      - "DB_ADDR=127.0.0.1:3306"
      - "LOG_LEVEL=info"
    restart: on-failure                # Restart，取值：no / on-success / on-failure / on-abnormal / on-abort / on-watchdog / always
    restart_sec: 5                     # RestartSec，重启间隔（秒）
    stop_signal: SIGTERM               # KillSignal，取值：SIGTERM（默认）/ SIGKILL / SIGINT / SIGHUP 等
    stop_timeout: 30                   # TimeoutStopSec，优雅停止宽限期（秒）
    memory_max: 2G                     # MemoryMax，大小带单位：2G / 500M / 1024K
    cpu_quota: 200%                    # CPUQuota，百分比：100% = 1 核，200% = 2 核
    std_output: journal                # StandardOutput/StandardError，取值：null（默认，裸写即可）/ none（兼容旧写法）/ journal / file:<path> / append:<path>
    depends_on:                        # After= + Wants=，仅控制启动顺序
      - redis
  redis:
    command: /opt/redis/redis-server /opt/redis/redis.conf
    working_dir: /opt/redis
    user: appuser
    restart: always
    std_output: null                   # 丢弃 stdout/stderr（裸写 null 即可）
    log_file: /var/log/redis/redis.log # 仅用于 logs 命令的 tail 提示
```

### 字段说明

> 所有字段值直接透传给对应 systemd 指令，**不做 compose 语义翻译**，取值以 systemd 为准。`restart` / `restart_sec` / `std_output` 未配置时使用代码默认值（`always` / `5` / `null`）。

| 字段 | 说明 | 写入的 systemd 指令 |
| --- | --- | --- |
| `description` | 服务描述（默认 `hr-compose service <name>`） | `Description` |
| `command` | 启动命令（**必须前台运行，不要 daemon**，必填） | `ExecStart` |
| `working_dir` | 工作目录 | `WorkingDirectory` |
| `user` / `group` | 运行身份 / 运行组（`user` 未配置默认注入执行 up 的真实用户） | `User` / `Group` |
| `environment` | 环境变量，每行一条 | `Environment=` |
| `restart` | 重启策略，取值：no / on-success / on-failure / on-abnormal / on-abort / on-watchdog / always（未配置默认 always） | `Restart` |
| `restart_sec` | 重启间隔秒数（默认 5） | `RestartSec` |
| `stop_signal` | 停止信号，取值：SIGTERM（默认）/ SIGKILL / SIGINT / SIGHUP / SIGQUIT / SIGUSR1 / SIGUSR2 等 | `KillSignal` |
| `stop_timeout` | 停止宽限期秒数（默认 90） | `TimeoutStopSec` |
| `memory_max` | 内存上限，大小带单位（2G / 500M / 1024K） | `MemoryMax` |
| `cpu_quota` | CPU 配额，百分比（100% = 1 核，200% = 2 核） | `CPUQuota` |
| `std_output` | 日志目标（null 默认 / none 兼容 / journal / file:`<path>` / append:`<path>`） | `StandardOutput=`、`StandardError=` |
| `log_file` | 外部日志文件路径（std_output 为 null 时 `logs` 提示用） | —（不参与 systemd 配置） |
| `depends_on` | 依赖的服务，仅控制启动顺序 | `After=` + `Wants=` |

### 日志目标（std_output）

| 取值 | 含义 | `hr-compose logs` 行为 |
| --- | --- | --- |
| `null`（默认） | 丢弃 stdout/stderr，日志由业务程序自行写入外部文件 | 提示用 `tail -f` 查看外部日志文件（配合 `log_file`） |
| `none` | `null` 的兼容写法（旧版本推荐，语义相同） | 同上 |
| `journal` | 写入 journald（非默认，需显式配置） | 执行 `journalctl -u <svcname>.service [-f]` |
| `file:<path>` | 覆盖写入（截断原文件） | 提示用 `tail -f <path>` 查看 |
| `append:<path>` | 追加写入 | 提示用 `tail -f <path>` 查看 |

> `file:` / `append:` 的文件由 systemd 以 `User=` 身份打开，需保证目录对该用户可写；`file:` 会截断已有文件。
>
> **日志清理**：`down` 会清空系统 journal（若编排中存在 journal 服务）；`clean [name]` 可手动清除日志（journal 清空 / file 截断 / null 提示）。注意 journald 不支持按 unit 删除，清空的是**整个系统 journal**。

---

## 注意事项

- **需要 root**：`up` / `down` / `start` / `stop` / `restart` / `clean` 操作 systemd（`/etc/systemd/system`）或日志，需 `sudo`。`ps` / `logs` / `config` 为只读，普通用户即可。
- **`stop` 与 `down` 的区别**：`stop` 只停进程，保留 unit 文件与 enable 状态（可随时 `start` 恢复）；`down` 会删除 unit 并 disable（下次要 `up` 重建）。临时停服用 `stop`。
- **服务名即 unit 文件名**：服务名只用小写字母、数字、`-`、`_`；避免与系统已有 unit 同名冲突。
- **删除/覆盖保护**：unit 文件头部有 `# MANAGED BY hr-compose` 标记；`down` 只删除带该标记的文件，`up` 也不会覆盖非该标记的同名 unit——防止误删/误覆盖同名系统服务。如确需让 hr-compose 托管某个同名 unit，先删除原文件再 `up`。
- **`std_output` 默认丢弃输出**：未配置时生成 `StandardOutput=null`，日志由业务程序自行管理；要进 journald 需显式 `std_output: journal`。丢弃输出直接裸写 `std_output: null` 即可（无需加引号）；`none` 为兼容旧写法。
- **`command` 必须前台运行**：值直接作为 `ExecStart`，业务程序不能 daemonize，否则 systemd 会认为服务未启动。
- **无 project 概念**：工具只管理当前目录 `hr-compose.yml` 中定义的服务，不扫描系统上其他 unit。
- **ps 彩色输出**：终端下状态自动着色，管道/重定向自动无色；设 `NO_COLOR=1` 可强制关闭。

---

## 问题排查

```bash
hr-compose config            # 校验 yml、预览生成的 unit 内容
systemctl status <svc>.service   # 查看 systemd 层面的真实状态
journalctl -u <svc>.service      # 查看 journal 日志
```

---

## License

[MIT](LICENSE) © 2026 labyte
