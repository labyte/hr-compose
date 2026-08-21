# hr-compose 离线安装与使用指南

> 适用于**目标服务器无外网 / 受限内网**环境。本文假设你已经拿到了发行包（tar.gz），从「安装 → 使用 → 更新卸载」全流程说明。

---

## 1. 前置条件

- **操作系统**：Linux（基于 systemd 的发行版，如 CentOS / Ubuntu / Debian）
- **权限**：安装到 `/usr/local/bin` 需要 root；日常 `up` / `down` / `start` / `stop` / `restart` / `clean` 也需要 root（操作 `/etc/systemd/system`），`ps` / `logs` / `config` 普通用户即可
- **自带命令**：`tar`（解压发行包），系统默认自带，无需额外安装
- **无外部依赖**：hr-compose 是单一静态二进制，离线环境零依赖，装好即用

### 平台与发行包对应关系

| 服务器 `uname -m` | 架构 | 选择发行包 |
| --- | --- | --- |
| `x86_64` / `amd64` | amd64 | `hr-compose_<版本>_linux_amd64.tar.gz` |
| `aarch64` / `arm64` | arm64 | `hr-compose_<版本>_linux_arm64.tar.gz` |

---

## 2. 安装

### 2.1 解压并安装到 `/usr/local/bin`（推荐，root）

```bash
tar -xzf hr-compose_1.0.0_linux_amd64.tar.gz      # 解压出根目录的 hr-compose 二进制
sudo install -m 0755 hr-compose /usr/local/bin/
rm -f hr-compose                                  # 清理解压出的临时文件（可选）
```

### 2.2 或安装到用户目录（无需 root）

适合没有 sudo 权限的用户态安装，把目录加入 `PATH`：

```bash
mkdir -p "$HOME/.local/bin"
tar -xzf hr-compose_1.0.0_linux_amd64.tar.gz
install -m 0755 hr-compose "$HOME/.local/bin/"
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc   # zsh 则写入 ~/.zshrc
source ~/.bashrc
```

### 2.3 安装命令补全（可选）

```bash
hr-compose completion install      # 自动写入当前 shell 配置（bash/zsh/fish），新开终端生效
# 或手动：hr-compose completion bash / zsh / fish
```

### 2.4 验证安装

```bash
hr-compose --help                  # 打印命令帮助
hr-compose --version               # 打印版本号
```

---

## 3. 使用过程

在**要管理服务所在的目录**下操作（hr-compose 只管理当前目录 `hr-compose.yml` 中定义的服务，无 project 概念）。

### 3.1 初始化编排文件

```bash
hr-compose init                    # 生成默认 hr-compose.yml 模板（已存在则不覆盖）
```

`init` 生成的模板默认包含 `api` / `web` 两个示例服务（最小配置，`web` 依赖 `api`），演示 `depends_on` 依赖写法，可直接按需修改或删除。

### 3.2 编辑 `hr-compose.yml`

最小配置只写 描述 / 启动命令 / 工作目录 三个字段，其余走代码默认值：

```yaml
services:
  app:
    description: 应用服务              # Description，服务描述
    command: /opt/myapp/app          # ExecStart，启动命令（必填，必须前台运行）
    working_dir: /opt/myapp          # WorkingDirectory，工作目录
```

未配置字段默认：`user` = 执行 up 的真实用户、`restart` = always（自动重启）、`restart_sec` = 5（间隔 5s）、`std_output` = null（丢弃输出）。

完整字段示例（含各字段取值说明）：

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
    stop_signal: SIGTERM               # KillSignal，取值：SIGTERM（默认）/ SIGKILL / SIGINT / SIGHUP / SIGQUIT / SIGUSR1 / SIGUSR2 等
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

> 字段值直接透传给对应 systemd 指令。`user` 未配置时自动注入执行 up 的真实用户；`restart` / `restart_sec` / `std_output` 未配置时使用代码默认值：`always` / `5` / `null`（丢弃输出）。要日志进 journald 需显式 `std_output: journal`；丢弃输出直接裸写 `std_output: null` 即可（无需加引号），`none` 为兼容旧写法。

### 3.3 预览生成的 unit（可选，只读）

```bash
hr-compose config                   # 校验 yml 并打印每个服务生成的 systemd unit 内容
hr-compose config api               # 只预览单个服务
```

### 3.4 启动服务

```bash
sudo hr-compose up                  # 生成 unit 到 /etc/systemd/system、enable、按依赖顺序 start，完成后自动展示状态表
```

`up` 是幂等的：重复执行不会重复创建或报错，unit 内容没变化时是空操作。想单点看状态随时执行：

```bash
hr-compose ps                       # 带边框状态表：NAME / STATUS / ENABLED / PID / MEMORY / UPTIME / CONFIG / DESCRIPTION
```

### 3.5 查看日志

```bash
hr-compose logs api                 # journal 模式：journalctl -u api.service
hr-compose logs api -f              # 实时跟踪
hr-compose logs redis               # 非 journal 模式：提示 tail 查看对应日志文件
```

### 3.6 日常运维

```bash
sudo hr-compose restart api         # 重启指定服务（不指定则全部）
sudo hr-compose stop api            # 停止服务（保留 unit 与开机启动，可随时 start 恢复）
sudo hr-compose start api           # 启动已安装的服务（需先 up 过）
sudo hr-compose enable api          # 设置开机启动（仅 enable，不启停）
sudo hr-compose disable api         # 取消开机启动（仅 disable，不删 unit）
sudo hr-compose clean api           # 清除日志（journal 清空 / file 截断）
```

### 3.7 停止并清理

```bash
sudo hr-compose down                # 逆序停止所有服务、disable、删除托管 unit 文件；若存在 journal 服务则清空系统 journal
```

> `down` 只删除带 `# MANAGED BY hr-compose` 标记的 unit，不会误删同名系统服务；若服务名与系统已有 unit 重名，`down` 会拒绝删除并报错。

### 命令速查表

| 命令 | 说明 | 是否需要 root |
| --- | --- | --- |
| `init` | 生成默认 `hr-compose.yml` 模板 | 否 |
| `config [name]` | 校验 yml、预览生成的 unit | 否 |
| `up` | 生成 unit + enable + start（幂等） | 是 |
| `ps` | 查看服务状态表 | 否 |
| `logs [name] [-f]` | 查看日志（`-f` 实时跟踪） | 否 |
| `restart [name]` | 重启服务 | 是 |
| `stop [name]` / `start [name]` | 停 / 启服务（保留 unit） | 是 |
| `enable [name]` / `disable [name]` | 设置 / 取消开机启动 | 是 |
| `clean [name]` | 清除服务日志 | 是 |
| `down` | 停止并清理全部服务、删除 unit | 是 |

全局参数 `--file <path>` 可指定编排文件路径（默认当前目录 `hr-compose.yml`）。

---

## 4. 更新与卸载（离线）

### 更新

```bash
# 1. 获取新版 tar.gz（有网的机器上下载后拷入，覆盖旧二进制）
# 2. 解压，覆盖安装旧二进制（路径同上一步，如 /usr/local/bin）
sudo install -m 0755 hr-compose /usr/local/bin/
# 3. 若新版生成的 unit 内容有变，重新执行一次（幂等）让配置生效
sudo hr-compose up
```

更新工具不会改动已有的 systemd unit 和运行中的服务。

### 卸载

```bash
sudo hr-compose down                      # 1. 停止并清理所有托管服务（只删带托管标记的 unit）
sudo rm /usr/local/bin/hr-compose         # 2. 删除二进制（自定义目录则删对应文件）
```

---

## 5. 常见问题

- **`hr-compose: command not found`**：确认二进制安装路径在 `PATH` 中（用户态安装后 `source ~/.bashrc`）。
- **`up` 报权限错误**：操作 `/etc/systemd/system` 需要 root，命令前加 `sudo`。
- **服务状态始终不是 running**：`command` 必须前台运行，业务程序不能 daemonize，否则 systemd 认为服务未启动。
- **日志看不到内容**：按 `std_output` 分发——未配置默认 `null`（丢弃输出），日志由业务程序自行写文件，用 `tail -f` 查看；`journal` 走 `journalctl`；`file:` / `append:` 查看对应文件（可用 `log_file` 字段让 `logs` 提示正确路径）。
- **`ps` 状态列为 `not-found`**：unit 未安装（还没 `up`，或已被 `down` 清理）时显示 `not-found`（未安装），与已安装但停止的 `stopped` 区分；仅当 systemctl 完全无输出时状态列显示 `-`，两种情况都不报错。
- **卸载后日志残留**：`down` 在存在 journal 服务时会清空**整个系统** journal（journald 不支持按 unit 删除），与 hr-compose 无直接关联的日志也会被清空，属已知行为。

更多细节与完整字段说明见项目根目录 [README](../README.md)。
