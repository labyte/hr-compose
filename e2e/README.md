# e2e 端到端测试

需要**真实的 Linux + systemd + root 权限**，普通单测环境跑不了，故独立成目录、默认不执行。

## 冒烟流程

在装了 systemd 的 Linux 机器上（root）：

```bash
# 1. 用示例编排文件生成并启动
cp ../testdata/valid.yml ./hr-compose.yml
hr-compose config          # 预览生成的 unit 内容，确认无误
hr-compose up              # 生成 + enable + start
hr-compose ps              # 查看状态
hr-compose logs redis      # 应提示 tail /var/log/redis/redis.log
hr-compose restart api
hr-compose down            # 停止 + 删除 unit

# 2. 验证系统状态
systemctl status demo-api.service demo-redis.service   # 应显示 inactive
ls /etc/systemd/system/demo-api.service                # 应不存在
```

## 断言点

- `up` 后 `demo-api.service`、`demo-redis.service` 存在且 `systemctl is-active` 为 active（unit 文件名带 `name: demo` 前缀）
- redis 先于 api 启动（`journalctl -u demo-api.service` 时间戳或启动日志）
- `down` 后 unit 文件被删除、服务 inactive
- 用非托管文件测试 `down` 保护：手工写一个 `xxx.service` 不带 `# MANAGED BY hr-compose` 标记，`down` 应拒绝删除
- journal 模式服务：`hr-compose logs api` 能跟到 journald 输出
- `up` 复核不谎报成功：`command` 指向不存在的文件时，`systemctl start` 仍返回 0（Restart=always 使其进入 auto-restart），`up` 必须点名该服务、转述 systemd 的失败口径（`203/EXEC`）、以非 0 退出，且其余服务照常启动；状态表不得透出 `[not set]` 这类内部字面量（`smoke.sh` 末段已断言）

## 真机行为验证脚本（非 CI）

这些脚本把静态分析得出的结论在带 systemd 的机器上跑出实证，用 `-b` 指定待验证的二进制（不指定则从 GitHub Releases 下载）：

| 脚本 | 验证内容 |
| --- | --- |
| `behavior-check.sh` | 改配置后重复 `up` 是否生效、`depends_on` 成环是否报错、字段值换行注入 unit、`command` 不存在的失败传播与复核（逐项 PASS/FAIL） |
| `followup-check.sh` | 跨段注入（`description` 里塞 `[Service]`/`ExecStartPre=`）是否真的被 systemd 执行 |
| `mem-check.sh` | `ps` 的内存读数与 `/proc/<pid>/smaps_rollup`、cgroup `memory.stat` 的对照 |
| `memformula-check.sh`、`pagecache-check.sh` | working set 口径（`memory.current` 减 `inactive_file`）在写大文件服务上的表现 |

## CI

`e2e/smoke.sh` 已接入 GitHub Actions 主 CI（ubuntu runner 自带 systemd，以 `sudo` 运行真实启停冒烟）。本地 Linux 也可直接执行：

```bash
bash e2e/smoke.sh
```
