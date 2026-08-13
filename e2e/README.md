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
systemctl status api.service redis.service   # 应显示 inactive
ls /etc/systemd/system/api.service           # 应不存在
```

## 断言点

- `up` 后 `api.service`、`redis.service` 存在且 `systemctl is-active` 为 active
- redis 先于 api 启动（`journalctl -u api.service` 时间戳或启动日志）
- `down` 后 unit 文件被删除、服务 inactive
- 用非托管文件测试 `down` 保护：手工写一个 `xxx.service` 不带 `# MANAGED BY hr-compose` 标记，`down` 应拒绝删除
- journal 模式服务：`hr-compose logs api` 能跟到 journald 输出

## CI

GitHub Actions 的 ubuntu runner 自带 systemd，可在此目录加一个 e2e job（`sudo systemctl` 场景需要 `--privileged` 或直接以 root 跑，暂缓接入主 CI）。
