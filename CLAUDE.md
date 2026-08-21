# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概览

hr-compose 是一个对标简化版 docker-compose 的 Go CLI：读取 `hr-compose.yml` 编排文件，在 Linux 裸机上基于 **systemd** 管理多个自研业务服务的启停与编排。核心设计原则是**复用 systemd，不重复造轮子**——进程监控、自启、重启、日志全交给 systemd，CLI 只做编排解析、unit 生成与 systemctl/journalctl 封装，保持薄。

命令：`init` / `up` / `start` / `stop` / `restart [name]` / `enable` / `disable` / `down` / `clean [name]` / `ps` / `logs [name] [-f]` / `config [name]`。无 project 概念，只管理当前目录 `hr-compose.yml` 中定义的服务，不扫描系统其他 unit。

## 设计原则（改动时须遵循）

1. **复用 systemd，不重复造轮子**：进程监控、自启、重启、日志全交给 systemd，CLI 只做编排解析、unit 生成与 systemctl/journalctl 封装。不要把进程守护逻辑搬进 Go 代码。
2. **开发量小，稳定性高**：进程管理这类易错逻辑全部委托 systemd，CLI 保持薄。新增功能优先考虑"能否用 systemd 原生能力实现"，而非自研。
3. **裸机优先**：生成 systemd unit 文件管理裸机服务，无容器。
4. **独立安装**：单一二进制、无外部依赖；发布即 goreleaser 多平台（linux/darwin × amd64/arm64）+ 在线安装脚本 `install.sh`。
5. **yml 即事实来源**：工具只管理当前目录 `hr-compose.yml` 中定义的服务，不扫描系统上其他 unit；无 project 概念。服务归属由 yml 所在目录界定。
6. **字段值直接透传**：`hr-compose.yml` 字段值直接作为 systemd 指令取值，不做 compose 语义翻译；取值的合法性与解释权归 systemd。

## 常用命令

```bash
make build        # 构建到 bin/hr-compose（version 经 ldflags -X main.version 注入）
make test         # go test ./...
make test-race    # go test -race ./...（CI 用这个）
make vet          # go vet ./...
make lint         # golangci-lint run
make fmt          # gofmt -w .
make cross        # 交叉编译 linux/amd64 + linux/arm64 + darwin/arm64
```

- 跑单个测试：`go test ./internal/engine -run TestOrderDependsFirst -v`
- CI（`.github/workflows/ci.yml`）依次跑：`go vet ./...` → `go test -race ./...` → golangci-lint → `make cross`。
- `e2e/smoke.sh` 是端到端冒烟（真实 systemd + root，单测环境跑不了），已接入 CI 的 e2e job（ubuntu runner 以 sudo 执行 up/ps/stop/start/down）；本地 Linux 可 `bash e2e/smoke.sh` 直接跑。

## 架构与数据流

依赖注入方向清晰，命令处理是单一管线：

```
internal/cli/     cobra 命令树，薄层。每个命令 RunE 里 load()（读配置 + 构造引擎）后调 engine 方法。**例外：`init` 不读配置**，直接调 `config.Init(path)` 生成模板
internal/config/  读取 + 校验 hr-compose.yml（yaml.v3，KnownFields(true)，未知字段直接报错）；含 `Init` 生成默认模板
internal/unit/    把单个服务配置渲染成 systemd unit 文本 + SHA256 内容 hash
internal/engine/  执行各命令动作（核心逻辑），持有 cfg 与 systemctl.Client
internal/systemctl/ 封装 systemctl / journalctl，Client 接口化便于测试注入 fake
```

- **`internal/config`**：`Service` 结构体字段名即 yaml 字段名（`working_dir`、`stop_timeout` 等）。字段值**直接透传给对应 systemd 指令，不做 compose 语义翻译**，取值以 systemd 为准。校验：services 非空、`command` 必填、`depends_on` 只能引用已定义服务、服务名字符集限小写字母/数字/`-`/`_`（服务名会拼成 unit 文件名）。`Load` 额外用 `yaml.Node` 保序记录 `ServiceOrder`（services 键在 yml 中的声明顺序，map 本身不保序），供 engine 启动/展示顺序使用。
- **`internal/unit`**：`Generate()` 把配置渲染成 unit 文本。头部写入 `# MANAGED BY hr-compose` 标记（`unit.ManagedMark`）+ 内容 hash。字段 → systemd 指令的映射关系：`description`→`Description`（空值经 `Service.EffectiveDescription` 回退为 `hr-compose service <name>`）、`command`→`ExecStart`、`working_dir`→`WorkingDirectory`、`user`→`User`（未配置经 `EffectiveUser` 自动注入执行 up 的真实用户，SUDO_USER 优先）、`restart`/`restart_sec`→`Restart`/`RestartSec`（未配置经 `EffectiveRestart`/`EffectiveRestartSec` 取默认 `always`/`5` 并恒写入）、`stop_signal`→`KillSignal`、`stop_timeout`→`TimeoutStopSec`、`memory_max`→`MemoryMax`、`cpu_quota`→`CPUQuota`、`environment`→每行 `Environment=`、`std_output`→`StandardOutput=`/`StandardError=`（未配置经 `EffectiveStdOutput` 默认 `null`）、`depends_on`→`After=`+`Wants=`。
- **`internal/engine`**：`order()` 对 `depends_on` 做拓扑排序（依赖者在前）；无依赖时按 yml 声明顺序（`ServiceOrder`）输出，声明顺序不可得时回退按名称排序；`up` 按序写 unit→daemon-reload→enable→start，用 `writeIfManaged` 保证幂等并拒绝覆盖非托管同名 unit；`down` 逆序 stop→disable→删文件（`removeIfManaged` 只删托管文件）。cli 层 `up` 命令在成功后再调 `e.Ps()` 展示服务状态。
- **`internal/systemctl`**：`Client` 是接口（`Enable/Disable/Start/Stop/Restart/DaemonReload/Show/ShowMany`），`Real` 是真实实现；`Show` 用 `systemctl show` 文本输出解析成 map（兼容老 systemd，不依赖 JSON）；`ShowMany` 一次批量查询多个 unit（`-p` 只取 ps 需要的属性），部分 unit 未加载时解析可用块、完全不可用才报错，ps 对批量缺失的 unit 回退逐服务 `Show`。

### 通用流程（各命令共享的不变式）

1. 读取 `hr-compose.yml`，yaml schema 校验（`KnownFields` 拒绝未知字段）。
2. 遍历 services，用 `unit.Generate` 为每个服务渲染 `xxx.service` 文本。
3. 写 `/etc/systemd/system/<svcname>.service`（头部带 `# MANAGED BY hr-compose` 标记 + 内容 hash）。
4. `systemctl daemon-reload`。

## 规划中的功能（勿与既有实现冲突）

来自迭代路线，尚未实现：`.env` 环境变量替换、日志磁盘空间保护（journald drop-in 限额、logrotate `copytruncate`）、目录隔离（多目录同服务名自动加前缀）、`scale` 多实例、校验 command 路径存在。设计这些功能时注意：`std_output` 语义、`ManagedMark` 删除保护、服务名即 unit 文件名的约束都已有测试覆盖。

## 关键安全约束（改动时勿破坏）

1. **unit 托管保护（删除 + 覆盖）**：`down` 用 `removeIfManaged` 只删带 `# MANAGED BY hr-compose` 标记的 unit；`up` 用 `writeIfManaged` 拒绝覆盖非该标记的同名 unit。两者共同防止误删/误覆盖同名系统服务。服务名可能碰巧与系统已有 unit 冲突，属已知风险。
2. **服务名即文件名**：`validateServiceName` 限制字符集，防止注入路径。
3. **root 权限**：操作 `/etc/systemd/system` 需要 sudo，`up`/`down`/`start`/`stop`/`restart` 必须提权；`ps`/`logs`/`config` 只读无需提权。

## 易错点

- **`-f` 简写归 `logs` 的 `--follow` 专用**：root 的 `--file` 有意不设简写，否则 cobra 合并子命令标志集时因重复 `-f` panic。回归测试 `TestLogsFlagShorthandNoConflict` 保护，新增全局/子命令标志时勿用 `-f` 简写。
- **`std_output` 默认丢弃输出**：未配置时 `EffectiveStdOutput()` 返回 `"null"`（生成 `StandardOutput=null`），日志由业务程序自行管理；要进 journald 需显式 `std_output: journal`。丢弃输出直接裸写 `std_output: null`（YAML null 字面量 → 字段为空 → 走空值默认 null，无需加引号）；`none` 为兼容旧写法，同样归一为 null。
- **`user` 省略时以执行 `up` 的用户运行**：`EffectiveUser()` 取 `SUDO_USER`（sudo 下）或当前用户。注意可复现性：同一 yml 被多人/CI 各自 `up` 会生成不同 `User=`（内容 hash 变 → 重写重启），共享编排建议显式 `user`。
- **`command` 必须前台运行**：值直接作为 `ExecStart`，业务程序不能 daemonize。
- **`ps` 的行为**：遍历 yml 中定义的服务逐个 `systemctl show`；unit 未加载时输出 `-` 空状态，不报错。
- **`ps` 用 go-pretty 渲染**：`text/tabwriter` 或手动定宽会把 ANSI 转义码计入列宽导致错位，改用 `github.com/jedib0t/go-pretty/v6`（会剥离 ANSI 计算可见宽度，可安全把 `text.Colors.Sprint` 生成的彩色字符串直接作为单元格）。列：NAME / STATUS（`mergedState` 合并 `ActiveState`+`SubState`+`LoadState` 为单字人话状态：not-found/running/exited/waiting/stopped/starting/restarting/stopping/reloading/failed，`LoadState=not-found` 表达未安装（unit 不存在，含 down 后），区别于已安装但停止的 stopped；activating 统一 starting、deactivating 统一 stopping、auto-restart 单独 restarting，罕见组合回退 `主/子`）/ ENABLED（`UnitFileState` 是否开机启动）/ PID / MEMORY / UPTIME（`ExecMainStartTimestampMonotonic` 减 `/proc/uptime` 算出主进程运行时长，`uptimeSince`+`formatUptime` 格式化，仅 `runningStates` 内状态展示）/ DESCRIPTION；unit 实际文件路径不在 ps 展示，`config` 命令预览时在段头标注完整路径（`filepath.Join(UnitDir, unitPath)`）。状态列用 `stateColors` 着色、`formatBytes` 格式化内存、`valueOrDash` 处理空值。勿改回 tabwriter 上色。
- **`logs` 的分发**：按 `std_output` 值分流——`journal` 执行 `journalctl -u <svc>.service`（`-f` 跟随）；`file:<p>` / `append:<p>` 执行 `tail` 查看（文件不存在给提示）；未配置（默认 null）与 `none`（旧写法）只打印 `tail` 提示。`log_file` 字段仅用于 null 模式的 tail 提示，不参与 systemd 配置。
- **journal 清理是全局的**：journald 不支持按 unit 删除日志，`down`（存在显式 `std_output: journal` 服务时；默认 null 不触发）与 `clean [name]` 对 journal 服务执行 `journalctl --rotate` + `--vacuum-size=1`，清空**整个系统 journal**（非仅 hr-compose 服务的日志）。`clean` 对 `file:`/`append:` 服务截断对应文件、`null` 仅提示。

## 测试约定

- 引擎测试通过 `fakeSys`（实现 `systemctl.Client`，记录调用序列）断言动作顺序；`UnitDir` 是包级**变量**（非 const）供测试覆盖为 `t.TempDir()`——新增依赖系统调用的逻辑时沿用该模式。
- `ps` 的彩色输出通过包级变量 `stdout`（写入目标）与 `colorOverride`（颜色强制开关 `""/always/never`）覆盖测试；颜色逻辑在 `color.go`（`colorsOn`/`stateColors`）。
- `config` 测试用 `t.TempDir()` 写 fixture yaml 后 `Load`，断言合法/非法用例。
- `testdata/` 下有 valid 与 3 个 invalid 样例，`e2e/README.md` 有完整冒烟流程。
- 注释、错误信息、README 均为中文，新增代码保持中文注释风格。
