# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概览

hr-compose 是一个对标简化版 docker-compose 的 Go CLI：读取 `hr-compose.yml` 编排文件，在 Linux 裸机上基于 **systemd** 管理多个自研业务服务的启停与编排。核心设计原则是**复用 systemd，不重复造轮子**——进程监控、自启、重启、日志全交给 systemd，CLI 只做编排解析、unit 生成与 systemctl/journalctl 封装，保持薄。

命令：`init [services-dir]` / `up [name...]` / `start [name...]` / `stop [name...]` / `restart [name...]` / `enable [name...]` / `disable [name...]` / `down [name...]` / `clean [name...]` / `ps` / `logs [name...] [-f]` / `config [name...] [--real]`。只管理当前目录 `hr-compose.yml` 中定义的服务，不扫描系统其他 unit；project 由顶层 `name:` 字段界定（必填），unit 文件名 = `<name>-<service>.service`，不同 project 的同名服务互不覆盖。所有接受服务名的命令（含 `up`/`down`）都支持一次指定多个；`up` 指定名字会带上其 `depends_on` 依赖（递归），`down` 只清理指定的。

## 设计原则（改动时须遵循）

1. **复用 systemd，不重复造轮子**：进程监控、自启、重启、日志全交给 systemd，CLI 只做编排解析、unit 生成与 systemctl/journalctl 封装。不要把进程守护逻辑搬进 Go 代码。
2. **开发量小，稳定性高**：进程管理这类易错逻辑全部委托 systemd，CLI 保持薄。新增功能优先考虑"能否用 systemd 原生能力实现"，而非自研。
3. **裸机优先**：生成 systemd unit 文件管理裸机服务，无容器。
4. **独立安装**：单一二进制、无外部依赖；发布即 goreleaser 多平台（linux/darwin × amd64/arm64）+ 在线安装脚本 `install.sh`。
5. **yml 即事实来源**：工具只管理当前目录 `hr-compose.yml` 中定义的服务，不扫描系统上其他 unit。服务归属由顶层 `name:` 字段（项目名）界定——unit 文件名带 `<name>-` 前缀，显式固定、改名移动目录不影响，不同 project 同名服务互不覆盖。
6. **字段值直接透传**：`hr-compose.yml` 字段值直接作为 systemd 指令取值，不做 compose 语义翻译；取值的合法性与解释权归 systemd。

## 常用命令

```bash
make build        # 构建到 bin/hr-compose（version 经 ldflags -X main.version 注入）
make install      # 拷 bin/hr-compose 到 $(PREFIX)/bin（默认 /usr/local/bin）；只拷贝不重建，故先 build 再 sudo make install
make test         # go test ./...
make test-race    # go test -race ./...（CI 用这个）
make vet          # go vet ./...
make lint         # golangci-lint run（工具为 v2 线，.golangci.yml 用 version: 2 格式）
make fmt          # gofmt -w .
make cross        # 交叉编译 linux/amd64 + linux/arm64 + darwin/arm64
```

- 跑单个测试：`go test ./internal/engine -run TestOrderDependsFirst -v`
- CI（`.github/workflows/ci.yml`）依次跑：`go vet ./...` → `go test -race ./...` → golangci-lint（`golangci-lint-action@v9` + `version: v2.15`）→ `make cross`。
- `e2e/smoke.sh` 是端到端冒烟（真实 systemd + root，单测环境跑不了），已接入 CI 的 e2e job（ubuntu runner 以 sudo 执行 up/ps/stop/start/down）；本地 Linux 可 `bash e2e/smoke.sh` 直接跑。

## 架构与数据流

依赖注入方向清晰，命令处理是单一管线：

```
internal/cli/     cobra 命令树，薄层。每个命令 RunE 里 load()（读配置 + 构造引擎）后调 engine 方法。**例外：`init` 不读配置**，无参直接调 `config.Init(path)` 生成模板；带 `services-dir` 调 `config.InitFromDir(dir, path)` 扫描目录生成服务列表
internal/config/  读取 + 校验 hr-compose.yml（yaml.v3，KnownFields(true)，未知字段直接报错）；含 `Init` 生成默认模板、`InitFromDir` 扫描目录生成服务列表
internal/unit/    把单个服务配置渲染成 systemd unit 文本 + SHA256 内容 hash
internal/engine/  执行各命令动作（核心逻辑），持有 cfg 与 systemctl.Client
internal/systemctl/ 封装 systemctl / journalctl，Client 接口化便于测试注入 fake
```

- **`internal/config`**：`Service` 结构体字段名即 yaml 字段名（`working_dir`、`stop_timeout` 等）。字段值**直接透传给对应 systemd 指令，不做 compose 语义翻译**，取值以 systemd 为准。`Config.Name` 是顶层 `name:` 字段（项目名，必填），unit 文件名前缀 `<name>-<service>.service`。校验：`name` 非空且字符集合法、services 非空、`command` 必填、`depends_on` 只能引用已定义服务、服务名与项目名字符集限小写字母/数字/`-`/`_`（都会拼进 unit 文件名）、`std_output` 的 `file:`/`append:` 后必须绝对路径（`stdOutputPath` 提取校验；相对路径由 clean/logs 按 CWD 解释、与 systemd 不一致，会截断/查看错文件）、`log_file` 非空时必须绝对路径（可带 glob；只被 logs 消费、不经 systemd，但相对路径同样随 CWD 漂移）。`Load` 额外用 `yaml.Node` 保序记录 `ServiceOrder`（services 键在 yml 中的声明顺序，map 本身不保序），供 engine 启动/展示顺序使用。`Init` 用 `defaultProjectName`（yml 所在目录 basename 的 sanitize 值）填充模板 `name:`；`InitFromDir(dir, path)` 扫描 dir 直接子文件夹（跳过文件、隐藏项、符号链接），每个子文件夹作一个服务：服务名=子文件夹名经 `sanitizeName`、`working_dir`=子文件夹绝对路径、`command` 依次探测同名可执行文件→`bin/<name>`→唯一可执行文件，探测不到回退占位 `./<name>` 并加 `# TODO` 注释，复用同一段 `templateHeader` 字段说明注释；子目录为空或 sanitize 后重名时报错，文件已存在不覆盖（同 `Init`）。
- **`internal/unit`**：`Generate(name, svc, project)` 把配置渲染成 unit 文本；`unit.Name(project, service)` 统一拼 unit 文件名（`<project>-<service>.service`，project 空时不加前缀，兼容直接构造 Config 的测试）。头部写入 `# MANAGED BY hr-compose` 标记（`unit.ManagedMark`）+ 内容 hash。字段 → systemd 指令的映射关系：`description`→`Description`（空值经 `Service.EffectiveDescription` 回退为 `hr-compose service <name>`）、`command`→`ExecStart`、`working_dir`→`WorkingDirectory`、`user`→`User`（未配置经 `EffectiveUser` 自动注入执行 up 的真实用户，SUDO_USER 优先）、`restart`/`restart_sec`→`Restart`/`RestartSec`（未配置经 `EffectiveRestart`/`EffectiveRestartSec` 取默认 `always`/`5` 并恒写入）、`stop_signal`→`KillSignal`、`stop_timeout`→`TimeoutStopSec`、`memory_max`→`MemoryMax`、`cpu_quota`→`CPUQuota`、`environment`→每行 `Environment=`、`std_output`→`StandardOutput=`/`StandardError=`（未配置经 `EffectiveStdOutput` 默认 `null`）、`depends_on`→`After=`+`Wants=`（依赖名带 project 前缀，同 project 内引用；`depends_on_required: true` 时改写 `After=`+`Requires=` 实现强依赖——被依赖启动失败则本服务不启动、被停止/失败联动停本服务，需配 `depends_on`，无则校验报错）、`start_delay`（秒，>0）→`ExecStartPre=/bin/sleep N`（位于 `ExecStart` 前，`After=` 依赖启动完成后才计时；计入 `TimeoutStartSec`；`Restart=always` 每次自动重启会再延时一次）、`start_timeout`→`TimeoutStartSec`（未配置经 `EffectiveStartTimeout` 取默认 90 并恒写入，与 systemd 默认一致；`start_delay` 较大时需同步放宽）。
- **`internal/engine`**：`allNames()` 取全部服务名的稳定基准顺序（yml `ServiceOrder`，不可得时按名排序）；`order(names []string)` 对给定名字做 `depends_on` 拓扑排序（依赖者在前，**子集外的依赖忽略**，输入顺序作无依赖服务的基准）；`resolve(names ...string)` 统一解析命令参数——无参→全部（拓扑序），有参→校验存在（未定义一次全部列出）、去重保序、子集内拓扑排序；`unitName(n)` 统一映射服务名→unit 文件名（`unit.Name(cfg.Name, n)`），各命令（up/down/start/stop/restart/enable/disable/ps/logs/config）均经它构造 unit 名；`up` 两趟：第一趟按序写 unit（`writeIfManaged` 保证幂等并拒绝覆盖非托管同名 unit，返回 `unitUnchanged/unitCreated/unitReplaced`），有 unit 被重写时先经 `fetchStates`（批量 + 逐服务回退）取"写入前是否在运行"，再统一 daemon-reload，第二趟按依赖顺序 enable + 启动——**内容变化且在运行中改用 restart**（start 对 active unit 是 no-op，新配置不会进进程），**状态查不到时一律按 restart 处理并打印警告**（退回 start 等于让改动静默失效却仍打印"已启动"；restart 对停着的服务即启动，是保守正确的默认），单个服务失败不中断其余服务但汇总报错；指定名字时经 `upTargets` 自动带上其 `depends_on` 传递闭包（docker-compose 语义，保证依赖先安装先启动）；收尾经 `checkStarted`（`upcheck.go`）复核启动结果并用同一份状态数据 `renderPs` 展示，未起来的服务逐条报错（见 `notRunningReason`），cli 层 `up` 不再另调 `Ps`；`start` 与 `restart` 只差动作与措辞，共用 `startAndVerify`（`startstop.go`）走同一套复核（`verifySubmitted`）——**复核通过、确认真起来了才打印 `<verb> <name> OK`**，systemctl 本身报错时保持 fail-fast 立刻停止提交其余服务（依赖没起来后面的多半也起不来）；`stop` 不需要复核（systemctl 作业失败会直接返回非 0）；`down` 逆序 stop→disable→删文件（`removeIfManaged` 只删托管文件），只清理指定的、不连带依赖，journal 清理经 `anyJournal(targets)` 只对本次目标集判断。
- **`internal/systemctl`**：`Client` 是接口（`Enable/Disable/Start/Stop/Restart/DaemonReload/Show/ShowMany/ClearJournal`），`Real` 是真实实现；`Show` 用 `systemctl show` 文本输出解析成 map（兼容老 systemd，不依赖 JSON）；`ShowMany` 一次批量查询多个 unit（`-p` 只取 `showProps`：ps 展示列 + up 复核用的 `Result/ExecMainCode/ExecMainStatus/NRestarts`），部分 unit 未加载时解析可用块、完全不可用才报错，`fetchStates` 对批量缺失的 unit 回退逐服务 `Show`。`Show`/`ShowMany` 用 `Output()` 只取 stdout，失败时经 `execErr` 把 `*exec.ExitError.Stderr` 拼进错误（`ShowMany` 的消息只带 `show` + unit 名，不带 `-p` 属性列表）——否则上层只剩 `exit status 1`，用户看不出是连不上 systemd、权限不足还是 unit 不存在。

### 通用流程（各命令共享的不变式）

1. 读取 `hr-compose.yml`，yaml schema 校验（`KnownFields` 拒绝未知字段）。
2. 遍历 services，用 `unit.Generate(name, svc, cfg.Name)` 为每个服务渲染 `<name>-<svcname>.service` 文本（`name` 为顶层项目名）。
3. 写 `/etc/systemd/system/<name>-<svcname>.service`（头部带 `# MANAGED BY hr-compose` 标记 + 内容 hash）。
4. `systemctl daemon-reload`。

## 规划中的功能（勿与既有实现冲突）

来自迭代路线，尚未实现：`.env` 环境变量替换、日志磁盘空间保护（journald drop-in 限额、logrotate `copytruncate`）、`scale` 多实例、校验 command 路径存在。设计这些功能时注意：`std_output` 语义、`ManagedMark` 删除保护、服务名/项目名即 unit 文件名（前缀）的约束都已有测试覆盖。

## 关键安全约束（改动时勿破坏）

1. **unit 托管保护（删除 + 覆盖）**：`down` 用 `removeIfManaged` 只删带 `# MANAGED BY hr-compose` 标记的 unit；`up` 用 `writeIfManaged` 拒绝覆盖非该标记的同名 unit。两者共同防止误删/误覆盖同名系统服务。项目前缀降低了 hr-compose 内部同名冲突，但服务名仍可能碰巧与系统已有 unit 冲突，属已知风险。
2. **服务名/项目名即文件名（前缀）**：`validateServiceName` / `validateProjectName` 限制字符集，防止注入路径。
3. **root 权限**：操作 `/etc/systemd/system` 需要 sudo，`up`/`down`/`start`/`stop`/`restart` 必须提权；`ps`/`logs`/`config` 只读无需提权。

## 易错点

- **`-f` 简写归 `logs` 的 `--follow` 专用**：root 的 `--file` 有意不设简写，否则 cobra 合并子命令标志集时因重复 `-f` panic。回归测试 `TestLogsFlagShorthandNoConflict` 保护，新增全局/子命令标志时勿用 `-f` 简写。
- **`sudo hr-compose` 报 command not found**：sudo 按 `/etc/sudoers` 的 `secure_path` 重置 PATH（Ubuntu 默认只有 `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin`），`$GOPATH/bin`、`$HOME/.local/bin` 都不在其中——装到那里只有普通 shell 能用，而 `up`/`down`/`start`/`stop`/`restart` 恰恰必须提权。故 `make install` 默认 `PREFIX=/usr/local`（可 `PREFIX=$HOME/.local` 装用户目录、`DESTDIR=` 做打包暂存），且**刻意不依赖 `build`**：sudo 下找不到 `go`，而以 root 跑 `go build` 会往用户 module cache 写 root 属主文件、害后续非 root 构建报权限错。正确顺序 `make build && sudo make install`；缺产物时 install 直接报"未找到 bin/hr-compose，请先执行 make build"而不是甩一堆 make 错。同一台机器别留多份（`~/go/bin` + `~/.local/bin` + `/usr/local/bin`），PATH 顺序会让旧版遮住新版，排查用 `which -a hr-compose` 逐个 `--version`。
- **`std_output` 默认丢弃输出**：未配置时 `EffectiveStdOutput()` 返回 `"null"`（生成 `StandardOutput=null`），日志由业务程序自行管理；要进 journald 需显式 `std_output: journal`。丢弃输出直接裸写 `std_output: null`（YAML null 字面量 → 字段为空 → 走空值默认 null，无需加引号）；`none` 为兼容旧写法，同样归一为 null。
- **`user` 省略时以执行 `up` 的用户运行**：`EffectiveUser()` 取 `SUDO_USER`（sudo 下）或当前用户。注意可复现性：同一 yml 被多人/CI 各自 `up` 会生成不同 `User=`（内容 hash 变 → 重写重启），共享编排建议显式 `user`。
- **`command` 必须前台运行**：值直接作为 `ExecStart`，业务程序不能 daemonize。
- **`systemctl start` 返回 0 ≠ 进程活着**：Type=simple 只要 fork 成功就算启动完成，`command` 路径不存在时 start 照样返回 0（`Restart=always` 让 unit 进入 auto-restart 循环）。真机实证见 `e2e/behavior-check.sh` 的 D 项。因此 `up`/`start`/`restart` 收尾都必须经 `checkStarted` 复核 systemd 状态再下结论（`start`/`restart` 经 `verifySubmitted` 复用同一套，别再各自打印 OK）：`stillStarting` 只对"还在启动过程中"（activating 且非 auto-restart，含 `start_delay` 的 start-pre）继续有界轮询（`upVerifyInterval` × `upVerifyRounds`，最长 ≈1.5s），`notRunningReason` 把 failed/inactive/auto-restart/not-found 判为未起来，原因只转述 systemd 自己给出的信息（`failureNote`：ExecMainStatus 200/203/217 专用码 → 指向 `working_dir`/`command`/`user`；判"主进程是不是自己退出"必须认 `systemctl show` 给出的**整数** `ExecMainCode=1`（glibc 的 CLD_EXITED），`exited` 这种字符串只存在于 `systemctl status` 的 `code=exited, status=203/EXEC` 里，见 `execMainExited`；`Result` 的 start-limit-hit/timeout/oom-kill 等），取不到信息时留空、绝不编造。systemctl 本身已报错的服务不再重复下结论；复核查询失败时不判服务失败（连状态都读不到，硬说"没起来"同样是编造），只如实报查询错误。
- **`ps` 的行为**：`Ps` = `resolve` → `fetchStates`（一次 `ShowMany` 批量查，缺失的 unit 回退逐服务 `Show`；只有 systemctl 完全不可用才报错）→ `renderPs`。三段分开是为了让 `up` 复核与展示复用同一次查询，不重复调 systemctl。unit 未加载时输出 `-` 空状态，不报错。`WatchPs`（`ps --watch`/`-w`）在 `Ps` 基础上加一个循环：先 resolve 一次固定目标集，之后按 `--interval`/`-i` 传入的间隔（默认 `DefaultWatchInterval`=2s）周期重查、原位重绘，直到收到 SIGINT（Ctrl+C）/SIGTERM（经 `signal.NotifyContext` 监听；测试用 `watchStop` 手动通道确定性结束循环）。原位重绘而非清整屏：每轮 `moveCursorUp(prevLines)`（`\x1b[<n>A`）把光标上移回上一张表顶覆盖重绘，`renderPs` 返回渲染行数供下轮上移；重绘后 `clearToEndOfScreen`（`\x1b[0J`）擦掉上一轮比本轮多出的行（仅非首轮清尾，避免抹掉用户此前的输出）。这些 ANSI 控制序列只在 stdout 是终端时生效（`termIsTerminal` 可测试覆盖），管道/重定向时不塞转义码；循环等待复用 `sleepFunc`。
- **`ps` 用 go-pretty 渲染**：`text/tabwriter` 或手动定宽会把 ANSI 转义码计入列宽导致错位，改用 `github.com/jedib0t/go-pretty/v6`（会剥离 ANSI 计算可见宽度，可安全把 `text.Colors.Sprint` 生成的彩色字符串直接作为单元格）。表头是 6 列（`psHeaders`）。列：NAME / STATUS（`mergedState` 合并 `ActiveState`+`SubState`+`LoadState` 为单字人话状态：not-found/running/exited/waiting/stopped/starting/restarting/stopping/reloading/failed，`LoadState=not-found` 表达未安装（unit 不存在，含 down 后），区别于已安装但停止的 stopped；activating 统一 starting、deactivating 统一 stopping、auto-restart 单独 restarting，罕见组合回退 `主/子`）/ ENABLED（`UnitFileState` 是否开机启动）/ PID（`pidOrDash`：未运行时 systemd 给 0，展示 `-`）/ MEMORY（`memoryCell`：取 cgroup v2 `memory.stat` 的 `anon`（匿名内存 = 堆/栈/私有数据），与系统监视器对单个进程显示的"内存"口径一致——不含可回收的文件页缓存（程序代码、共享库、读过的数据文件），也不含共享内存/内核 slab/页表等开销；`serviceMemory` 优先读 anon，读不到 cgroup（v1/混合层级/无权限）或 stat 无 anon 行时回退 `MemoryCurrent`，`memCurrentBytes` 把 `[not set]`/2^64 哨兵值统一归为 `-`，绝不透出 systemd 内部字面量；配了 `memory_max` 追加 ` / 上限`，占用比按同一 anon 算——anon 逼近上限才真会 OOM，文件页缓存可被内核回收、逼近上限不触发 OOM，用含缓存的 current 算反而会对只做文件 I/O 的服务虚报警告；`memoryWarn` 80%/90% 给 `!`/`!!` 标记与配色；上限解析为 0（`memory_max: "0"`/`"0K"`）时只展示 ` / 0B` 不算占用比，否则除零会让只读的 ps panic）/ UPTIME（`ExecMainStartTimestampMonotonic` 减 `/proc/uptime` 算出主进程运行时长，`uptimeSince`+`formatUptime` 格式化，仅 `runningStates` 内状态展示）。**已移除 DESCRIPTION 列**（描述仍写进 unit 的 `Description=`，完整内容看 yml 或 `config` 命令）；unit 实际文件路径也不在 ps 展示，`config` 命令默认预览时在段头标注"预览"与完整路径（`filepath.Join(UnitDir, unitPath)`），`config --real` 读取并打印磁盘上实际的 unit 文件（段头标注"实际文件"，文件不存在时给出提示而非报错）。状态列用 `stateColors` 着色、`formatUintBytes` 格式化内存、`valueOrDash` 处理空值；边框字符经 `dimBorders` 统一着暗灰（`borderColors`=亮黑，终端改不了字形笔画粗细，暗灰是"线条更细更暗"的唯一杠杆，数据单元格因此更突出），渲染先入缓冲区再着边框色写 `stdout`；`syncTextColors` 把颜色开关同步给 go-pretty text 包的全局开关（text 包 init 时按 `TERM=dumb`/`NO_COLOR` 等环境定生死，不同步则测试沙箱里 `colorOverride=always` 名不副实、`Colors.Sprint` 变 no-op）。勿改回 tabwriter 上色。
- **`name` 必填且显式固定**：顶层 `name:` 缺失时 `Load` 报错（所有命令都先 Load）。项目名写死于文件、不随目录变化——重命名/移动目录不影响 unit 名，但**这是破坏性变更**：旧 yml 需补 `name:`，旧 `<svc>.service` 不会被新 `down` 清理，升级需先 `down` 旧服务或手动清理。
- **`logs` 的分发**：按 `std_output` 值分流——`journal` 执行 `journalctl -u <name>-<svc>.service`（`-f` 跟随）；`file:<p>` / `append:<p>` 执行 `tail` 查看（文件不存在给提示）；`null` 配了 `log_file` 时**直接 tail 业务日志**（`resolveLogFile`：含 glob 通配符经 `filepath.Glob` 展开取 mtime 最新者——NLog 一类业务库按日期切文件，mtime 最新即当天在写的；mtime 相同平局取 glob 排序靠后者，ISO 日期文件名字典序即时间序；解析不到给提示不报错），未配 `log_file` 才打印 `tail` 提示。`log_file` 字段仅服务 logs 命令（须绝对路径、可带 glob），不参与 systemd 配置。
- **journal 清理是全局的**：journald 不支持按 unit 删除日志，`down`（本次清理的目标服务中存在显式 `std_output: journal` 时；默认 null 不触发，指定子集时只判断目标集，不会因其他服务的 journal 误清）与 `clean [name...]` 对 journal 服务执行 `journalctl --rotate` + `--vacuum-size=1`，清空**整个系统 journal**（非仅 hr-compose 服务的日志）。`clean` 的破坏面超出单个服务，故清 journal 前经 `confirmJournalClear` 确认：交互环境提示 `[y/N]`（仅 y/yes 放行），非交互环境必须 `--yes`/`-y`（cli 层 `cleanYes`）否则跳过并提示——脚本/CI 里一条命令静默抹全系统 journal 不可接受；`stdin`/`stdinIsTTY` 为包级变量供测试覆盖。`clean` 对 `file:`/`append:` 服务截断对应文件（`truncateLog`）：`file:` 模式 systemd 不开 O_APPEND、运行中进程的 fd 写偏移不随截断归零，后续写入留 NUL 空孔，故经 `serviceRunning`（查不到状态按未运行，不编造）判运行中时警告 restart 一次，`append:`（O_APPEND）安全不警告；单服务失败不中断其余、`errors.Join` 汇总返回。`null` 仅提示。
- **lint 的三处版本必须成套匹配**：`.golangci.yml` 是 `version: "2"` 格式，只有 golangci-lint v2 线能读（v1 会报 `unsupported version of the configuration`），CI 侧对应 `golangci-lint-action@v9`（action 从 v7 起才支持 linter v2；`@v6` 配 `version: latest` 装的仍是 v1）。配置文件与 action 版本要同笔改、同笔回滚——action 默认 `verify: true`，会按所装 linter 版本的 JSON Schema 校验配置，一半新一半旧直接失败。配置里另有两处是为**复刻 v1 检查集**而显式写的，删掉就冒新错：`linters.exclusions.presets` 的 `std-error-handling`（v1 默认排除 `fmt.Fprintf(stdout, ...)`/`os.Close` 一类，v2 不再自动应用，去掉即 7 条 errcheck，已在 `startstop.go` 上实测复现）与 `linters.settings.staticcheck.checks: ["all", "-QF*"]`（v1 的 staticcheck 不跑 QF，去掉即 `validateServiceName`/`validateProjectName` 的两处 `!(...)` 报 QF1001）。linter 集合未写 `linters.enable`，走 v2 默认的 standard 集（errcheck/govet/ineffassign/staticcheck/unused，等价 v1 默认集：deadcode 已删除由 unused 覆盖、gosimple 已并入 staticcheck）。本地还有个反向坑：v1 版 linter 跑在 Go 1.24+ 工具链上，内置的旧 go/types 读不了新 stdlib 导出数据，会报一堆 `undefined: yaml (typecheck)` 假错，需钉 `GOTOOLCHAIN=go1.22.x`（反之 v2 在 Go 1.22 下工作正常，故 CI 的 `go-version: "1.22"` 不必跟着升）。

## 测试约定

- 引擎测试通过 `fakeSys`（实现 `systemctl.Client`，记录调用序列）断言动作顺序；`UnitDir` 是包级**变量**（非 const）供测试覆盖为 `t.TempDir()`——新增依赖系统调用的逻辑时沿用该模式。直接构造 `Config` 不带 `Name` 时 unit 名不加前缀（既有断言不受影响），project 前缀逻辑用 `cfg.Name` 非空的用例（如 `TestUnitNamePrefix`）覆盖。
- 需要按 unit 指定状态的用例用 `stateSys`：`states` 是"写 unit 之前"的状态（决定 start 还是 restart），调用过 `Start`/`Restart` 之后改用 `after`（未指定即视为启动成功 running），使只关心动作序列的用例不被收尾复核误伤；模拟"start 返回 0 但进程没活"就用 `after` 填 `activating/auto-restart` 等状态。`up` 复核的等待经包级变量 `sleepFunc` 覆盖，`TestMain` 已统一置为空操作（fake 状态即时给定，真睡只拖慢测试）。
- 模拟"状态查不到"用 `brokenThenOKSys`（前 N 次查询失败后恢复，验证 up 退回 restart 而非 start）与 `showFailSys`（恒失败，验证 up 如实报错不谎报成功）。
- `internal/systemctl` 的测试用 `fakeSystemctl(t, stdout, stderr, exitCode)` 在 `PATH` 最前面放假脚本（`t.Setenv` 自动恢复），使 `Real.Show/ShowMany` 的输出解析与错误包装在没有 systemd 的环境（CI 容器、非 systemd 主机）里也能跑，不必依赖真机。
- `ps` 的彩色输出通过包级变量 `stdout`（写入目标）与 `colorOverride`（颜色强制开关 `""/always/never`）覆盖测试；颜色逻辑在 `color.go`（`colorsOn`/`stateColors`）。
- `config` 测试用 `t.TempDir()` 写 fixture yaml 后 `Load`，断言合法/非法用例。
- `testdata/` 下有 valid 与 3 个 invalid 样例，`e2e/README.md` 有完整冒烟流程。
- 注释、错误信息、README 均为中文，新增代码保持中文注释风格。
