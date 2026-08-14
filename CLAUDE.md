# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概览

hr-compose 是一个对标简化版 docker-compose 的 Go CLI：读取 `hr-compose.yml` 编排文件，在 Linux 裸机上基于 **systemd** 管理多个自研业务服务的启停与编排。核心设计原则是**复用 systemd，不重复造轮子**——进程监控、自启、重启、日志全交给 systemd，CLI 只做编排解析、unit 生成与 systemctl/journalctl 封装，保持薄。

命令：`init` / `up` / `down` / `restart [name]` / `ps` / `logs [name] [-f]` / `config`。无 project 概念，只管理当前目录 `hr-compose.yml` 中定义的服务，不扫描系统其他 unit。

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
- `e2e/` 是端到端测试，需要真实 Linux + systemd + root 权限，**默认不执行**，单测环境跑不了。

## 架构与数据流

依赖注入方向清晰，命令处理是单一管线：

```
internal/cli/     cobra 命令树，薄层。每个命令 RunE 里 load()（读配置 + 构造引擎）后调 engine 方法。**例外：`init` 不读配置**，直接调 `config.Init(path)` 生成模板
internal/config/  读取 + 校验 hr-compose.yml（yaml.v3，KnownFields(true)，未知字段直接报错）；含 `Init` 生成默认模板
internal/unit/    把单个服务配置渲染成 systemd unit 文本 + SHA256 内容 hash
internal/engine/  执行各命令动作（核心逻辑），持有 cfg 与 systemctl.Client
internal/systemctl/ 封装 systemctl / journalctl，Client 接口化便于测试注入 fake
```

- **`internal/config`**：`Service` 结构体字段名即 yaml 字段名（`working_dir`、`stop_timeout` 等）。字段值**直接透传给对应 systemd 指令，不做 compose 语义翻译**，取值以 systemd 为准。校验：services 非空、`command` 必填、`depends_on` 只能引用已定义服务、服务名字符集限小写字母/数字/`-`/`_`（服务名会拼成 unit 文件名）。
- **`internal/unit`**：`Generate()` 把配置渲染成 unit 文本。头部写入 `# MANAGED BY hr-compose` 标记（`unit.ManagedMark`）+ 内容 hash。字段 → systemd 指令的映射关系：`command`→`ExecStart`、`working_dir`→`WorkingDirectory`、`restart_sec`→`RestartSec`、`stop_signal`→`KillSignal`、`stop_timeout`→`TimeoutStopSec`、`memory_max`→`MemoryMax`、`cpu_quota`→`CPUQuota`、`environment`→每行 `Environment=`、`std_output`→`StandardOutput=`/`StandardError=`、`depends_on`→`After=`+`Wants=`。
- **`internal/engine`**：`order()` 对 `depends_on` 做拓扑排序（依赖者在前，排序后按名字稳定输出）；`up` 按序写 unit→daemon-reload→enable→start；`down` 逆序 stop→disable→删文件。`writeIfChanged` 保证 up 幂等（内容不变不重写）。
- **`internal/systemctl`**：`Client` 是接口（`Enable/Disable/Start/Stop/Restart/DaemonReload/Show`），`Real` 是真实实现；`Show` 用 `systemctl show` 文本输出解析成 map（兼容老 systemd，不依赖 JSON）。

### 通用流程（各命令共享的不变式）

1. 读取 `hr-compose.yml`，yaml schema 校验（`KnownFields` 拒绝未知字段）。
2. 遍历 services，用 `unit.Generate` 为每个服务渲染 `xxx.service` 文本。
3. 写 `/etc/systemd/system/<svcname>.service`（头部带 `# MANAGED BY hr-compose` 标记 + 内容 hash）。
4. `systemctl daemon-reload`。

## 规划中的功能（勿与既有实现冲突）

来自迭代路线，尚未实现：`.env` 环境变量替换、`stop`/`start`（停不删 unit）、日志磁盘空间保护（journald drop-in 限额、logrotate `copytruncate`）、目录隔离（多目录同服务名自动加前缀）、`scale` 多实例、校验 command 路径存在、命令/路径/服务名补全。设计这些功能时注意：`std_output` 语义、`ManagedMark` 删除保护、服务名即 unit 文件名的约束都已有测试覆盖。

## 关键安全约束（改动时勿破坏）

1. **down 删除保护**：`removeIfManaged` 只删除内容以 `# MANAGED BY hr-compose` 开头的 unit 文件，防止误删同名系统服务。服务名可能碰巧与系统已有 unit 冲突，属已知风险。
2. **服务名即文件名**：`validateServiceName` 限制字符集，防止注入路径。
3. **root 权限**：操作 `/etc/systemd/system` 需要 sudo，`up`/`down`/`restart` 必须提权；`ps`/`logs`/`config` 只读无需提权。

## 易错点

- **`-f` 简写归 `logs` 的 `--follow` 专用**：root 的 `--file` 有意不设简写，否则 cobra 合并子命令标志集时因重复 `-f` panic。回归测试 `TestLogsFlagShorthandNoConflict` 保护，新增全局/子命令标志时勿用 `-f` 简写。
- **`std_output: null` 必须加引号**：未加引号的 `null` 是 YAML null 字面量，等价于"未配置"，会被当作默认 `journal`。要真正丢弃输出需写 `std_output: "null"`。`EffectiveStdOutput()` 处理这一语义。
- **`command` 必须前台运行**：值直接作为 `ExecStart`，业务程序不能 daemonize。
- **`ps` 的行为**：遍历 yml 中定义的服务逐个 `systemctl show`；unit 未加载时输出 `-` 空状态，不报错。
- **`ps` 用手动定宽而非 tabwriter**：`text/tabwriter` 会把 ANSI 转义码计入列宽导致错位，`ps` 采用 `%-*s` 手动填充列宽 + 状态列后包颜色码（先填充再着色，颜色不参与宽度计算）。勿改回 tabwriter 上色。
- **`logs` 的分发**：按 `std_output` 值分流——`journal`（默认）执行 `journalctl -u <svc>.service`（`-f` 跟随）；`file:<p>` / `append:<p>` / `"null"` 只打印 `tail -f` 提示，不真正执行 tail。`log_file` 字段仅用于 null 模式的 tail 提示，不参与 systemd 配置。

## 测试约定

- 引擎测试通过 `fakeSys`（实现 `systemctl.Client`，记录调用序列）断言动作顺序；`UnitDir` 是包级**变量**（非 const）供测试覆盖为 `t.TempDir()`——新增依赖系统调用的逻辑时沿用该模式。
- `ps` 的彩色输出通过包级变量 `stdout`（写入目标）与 `colorOverride`（颜色强制开关 `""/always/never`）覆盖测试；颜色逻辑在 `color.go`（`colorsOn`/`stateColor`），不新增外部依赖。
- `config` 测试用 `t.TempDir()` 写 fixture yaml 后 `Load`，断言合法/非法用例。
- `testdata/` 下有 valid 与 3 个 invalid 样例，`e2e/README.md` 有完整冒烟流程。
- 注释、错误信息、README 均为中文，新增代码保持中文注释风格。
