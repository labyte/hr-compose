# hr-compose

> 对标 docker-compose 的本地服务编排工具（CLI）。

## 项目简介

hr-compose 是一个自研 CLI 工具，读取 YAML 编排文件，在 Linux 裸机上管理多个自研业务程序的启停与编排，能力对标简化版 docker-compose：

```bash
hr-compose up / down / restart / ps / logs / config
```

## 设计目标与原则

1. **复用 systemd，不重复造轮子**：不自己实现进程守护，复用 Linux systemd 完成进程监控、自启、重启、日志；CLI 只负责编排解析、生成 service、封装 systemctl / journalctl 调用。
2. **开发量小，稳定性高**：进程管理这类易错逻辑全部交给 systemd，CLI 保持薄。
3. **裸机优先**：生成 systemd unit 文件，无容器。
4. **独立安装**：单一二进制，无外部依赖。
5. **yml 即事实来源**：只管理当前目录 `hr-compose.yml` 中定义的服务，不扫描系统上其他 unit；无 project 概念。

## 核心能力对标 docker-compose

| docker-compose   | hr-compose            | 实现原理                                   |
| ---------------- | --------------------- | ------------------------------------------ |
| docker-compose.yml | hr-compose.yml           | yaml 编排文件，定义多个服务                 |
| up -d            | `hr-compose up`            | 解析 yaml，批量生成 .service，enable + start |
| down             | `hr-compose down`          | stop、disable、删除 systemd unit 文件      |
| ps               | `hr-compose ps`            | 读取 systemd 状态，格式化输出列表           |
| restart          | `hr-compose restart [name]` | systemctl restart                          |
| logs -f          | `hr-compose logs [name] -f` | 按 `std_output` 分发：journal→journalctl，非 journal→提示 tail |
| config           | `hr-compose config`        | 校验 yaml，输出生成的 service 内容         |

## CLI 命令

| 命令                  | 说明             | 备注                             |
| --------------------- | ---------------- | -------------------------------- |
| `hr-compose up`            | 启动全部服务     | 遵循 `depends_on` 启动顺序       |
| `hr-compose down`          | 停止并清理服务   | 删除 unit 文件                   |
| `hr-compose restart [name]`| 重启服务         | 不指定 name 则重启全部           |
| `hr-compose ps`            | 列出服务状态     | 遍历 yml 服务，解析 `systemctl show` 状态 |
| `hr-compose logs [name] -f`| 查看日志         | 按 `std_output` 分发：journal→journalctl，非 journal→提示 tail |
| `hr-compose config`        | 校验并预览配置   | 打印生成的 service 内容          |

## 编排文件 hr-compose.yml

编排文件放在项目目录，默认读取当前目录下的 `hr-compose.yml`。

```yaml
# hr-compose.yml 示例
version: "1.0"
services:
  api:
    command: /opt/myapp/api
    working_dir: /opt/myapp
    user: appuser
    group: appuser
    environment:
      - "DB_ADDR=127.0.0.1:3306"
      - "LOG_LEVEL=info"
    restart: on-failure
    restart_sec: 5
    stop_timeout: 30
    memory_max: 2G
    cpu_quota: 200%
    depends_on:
      - redis
  redis:
    command: /opt/redis/redis-server /opt/redis/redis.conf
    working_dir: /opt/redis
    user: appuser
    restart: always
    std_output: "null"
    log_file: /var/log/redis/redis.log
```

### 字段说明

> 取值规则：所有字段的值直接透传给对应 systemd 指令，不做 compose 语义翻译；取值以 systemd 为准。

| 字段             | 说明                                   | 写入的 systemd 指令 |
| ---------------- | -------------------------------------- | ----------------- |
| `command`        | 可执行程序启动命令（**必须前台运行，不要 daemon**） | `ExecStart`       |
| `working_dir`    | 工作目录                               | `WorkingDirectory` |
| `user` / `group` | 运行身份 / 运行组                      | `User` / `Group`  |
| `environment`    | 环境变量，直接写入 service             | `Environment=`    |
| `restart`        | 重启策略，取值即 `Restart=`（no / on-success / on-failure / always 等） | `Restart` |
| `restart_sec`    | 重启间隔秒数                           | `RestartSec`      |
| `stop_signal`    | 停止信号（默认 SIGTERM）               | `KillSignal=`      |
| `stop_timeout`   | 停止宽限期秒数（默认 90）              | `TimeoutStopSec=`  |
| `memory_max`     | 内存上限                               | `MemoryMax`       |
| `cpu_quota`      | CPU 配额                               | `CPUQuota`        |
| `std_output`     | 日志目标，取值即 `StandardOutput=`（journal / "null" / file:&lt;path&gt; / append:&lt;path&gt;）。**null 需加引号**，否则 YAML 视为 null 字面量（=未配置→journal） | `StandardOutput=`、`StandardError=` |
| `log_file`       | （可选）外部日志文件路径，仅用于 `logs` 提示 | —（不参与 systemd 配置） |
| `depends_on`     | 依赖的服务，仅控制启动顺序，依赖失败不阻塞 | `After=` + `Wants=`          |

## 命名约定

- 无 project 概念：服务归属由当前目录的 `hr-compose.yml` 界定，工具只管理该文件定义的服务。
- Service 文件名：`/etc/systemd/system/<svcname>.service`（直接用配置中的服务名）。
- ⚠️ 服务名需避免与系统已有 unit 冲突。生成的 unit 文件头部写入 `# MANAGED BY hr-compose` 标记；`down` 删除前校验该标记，防止误删同名系统服务。

## 内部工作流程

### 通用流程

1. 读取当前目录 `hr-compose.yml`，做 yaml schema 校验。
2. 遍历 services，为每个服务生成对应的 `xxx.service` systemd 单元文本。
3. 将生成的 service 文件输出到 `/etc/systemd/system/<svcname>.service`（头部写入 `# MANAGED BY hr-compose` 标记与内容 hash）。
4. 执行 `systemctl daemon-reload`。

### 各命令行为

- **up**：依次执行 enable + start，遵循 `depends_on` 的启动顺序（生成 `After=` + `Wants=`）。重复 up 幂等；比对 unit 内容 hash，若与 yml 不一致先 daemon-reload 并提示配置已变更，必要时 restart 生效。
- **down**：对 yml 中每个服务 stop → disable → 删除 service 文件（删除前校验 `# MANAGED BY hr-compose` 标记）→ daemon-reload。
- **ps**：遍历 yml 服务列表，逐个调用 `systemctl show <svcname>.service` 读取状态（ActiveState / SubState / ExecMainPID / MemoryCurrent 等），格式化表格输出；只列当前 yml 定义的服务。
- **logs**：按服务 `std_output` 分发查看命令——`journal` 封装 `journalctl -u <svcname>.service`（`-f` 实时跟踪）；`file:` / `append:` / `"null"` 提示用户用 `tail -f` 查看对应日志文件。
- **config**：输出生成的 service 内容，用于调试。

> ⚠️ 工具需要 root 权限操作 `/etc/systemd/system`，执行 `hr-compose up` / `hr-compose down` 需要 `sudo`。

## 日志功能设计（std_output）

日志目标按服务配置。`std_output` 决定业务程序 stdout / stderr 的去向，`hr-compose logs` 依据该配置分发查看命令。

### 取值与 logs 行为

| std_output | 含义 | 写入指令 | `hr-compose logs` 行为 |
| --- | --- | --- | --- |
| `journal`（默认） | 写入 journald | `StandardOutput=journal`、`StandardError=journal` | 执行 `journalctl -u <svcname>.service [-f]` |
| `"null"` | 丢弃 stdout/stderr，由业务程序写外部日志文件。**YAML 里必须加引号** | `StandardOutput=null`、`StandardError=null` | 提示用户用 `tail -f` 查看外部指定的日志文件 |
| `file:<path>` | 覆盖写入（截断原文件） | `StandardOutput=file:<path>`、`StandardError=file:<path>` | 提示用户用 `tail -f <path>` 查看 |
| `append:<path>` | 追加写入 | `StandardOutput=append:<path>`、`StandardError=append:<path>` | 提示用户用 `tail -f <path>` 查看 |

> 配套字段 `log_file`（可选）：`std_output: "null"` 时声明业务程序自行写入的日志文件路径，仅用于 `logs` 给出具体的 `tail` 提示，不参与 systemd 配置。
>
> `file:` / `append:` 的文件由 systemd 以 `User=` 身份打开，需保证目录对该用户可写（chown）；`file:` 会截断已有文件，`append:` 追加。

### 磁盘空间保护（防爆盘）

目标：默认禁止日志无限占用系统盘（根分区），避免日志过多占满空间。

1. **journal 限额自动配置**：`hr-compose up` 时自动写入 journald drop-in `/etc/systemd/journald.conf.d/99-hr-compose.conf`，设置 `SystemMaxUse=`（默认 2G）与 `SystemKeepFree=`（默认 1G），确保 journal 有硬上限；本机已配置则跳过。
2. **可选 `Storage=volatile`**：高频临时日志可配 volatile，journal 写入内存 tmpfs（`/run/log/journal`），完全不占系统盘、重启即清（日志易失，需接受）。
3. **文件落盘可分流**：`std_output: file:<path>` / `append:<path>` 把日志写到独立挂载的数据盘，不占系统盘。
4. **null 模式不落盘**：`std_output: "null"` 完全不写系统盘，日志由业务程序自行管理。
5. **配套 logrotate 轮转**：file 模式生成 logrotate 配置（按大小 / 天轮转、限定保留份数）；systemd 以追加方式持有文件 fd，轮转必须 `copytruncate`，压缩时 `su <user>`。

> 文件落盘路径与磁盘挂载由运维规划；`null` 模式的日志轮转由业务程序自己负责。

## 技术选型

- 语言：**Go**（静态编译，单一二进制，独立安装）。

## 迭代路线

### V1.0 基础版（先实现）

- 解析 hr-compose.yml，生成 systemd service
- up / down / ps / restart / logs
- `depends_on` 启动顺序控制（`After=` + `Wants=`）
- 优雅停止：`stop_signal` / `stop_timeout`
- `std_output` 日志目标配置（journal / null / file / append）与 `logs` 分发
- config 子命令：校验 yaml，打印生成的 service 内容，用于调试

### V1.1 增强

- 支持多环境 `.env` 文件，环境变量替换（类似 compose .env）
- `hr-compose stop` 停止不删除 unit；`hr-compose start`
- 状态输出美化表格
- 校验 command 路径是否存在
- up 幂等：unit 内容 hash 比对，配置变更提示 restart
- 日志磁盘空间保护：journald 限额自动配置（drop-in）、logrotate 生成（copytruncate）

### V1.2

- 目录隔离：多目录可共存，同名服务自动加前缀避免互相覆盖
- `hr-compose scale` 简单多实例
- 导出当前运行配置

## 其他要求

- 补全功能：命令补全、路径补全、服务名补全
- 能独立安装，不要依赖其他工具
