#!/usr/bin/env bash
# e2e 冒烟测试：需要真实 systemd + root（CI 的 ubuntu runner 与本地 Linux 均可执行）。
# 用法：bash e2e/smoke.sh [hr-compose 二进制路径]
set -euo pipefail

BIN="${1:-bin/hr-compose}"
case "$BIN" in
  /*) ;;
  *) BIN="$(pwd)/$BIN" ;;
esac

WORKDIR="$(mktemp -d)"
cd "$WORKDIR"
trap 'sudo "$BIN" down >/dev/null 2>&1 || true; rm -rf "$WORKDIR"' EXIT

cat > hr-compose.yml <<'YAML'
name: smoke-demo
services:
  demo1:
    command: /usr/bin/sleep infinity
    description: e2e demo 1
  demo2:
    command: /usr/bin/sleep infinity
    depends_on: [demo1]
YAML

echo "==> 校验 yml 预览 unit"
"$BIN" config | grep -q "预览"
# up 前磁盘上尚无 unit 文件，--real 应提示不存在而非报错
"$BIN" config --real | grep -q "文件不存在"

echo "==> up 并验证状态"
# unit 文件名带 project 前缀（name: smoke-demo）
sudo "$BIN" up
test "$(systemctl is-active smoke-demo-demo1.service)" = "active"
test "$(systemctl is-active smoke-demo-demo2.service)" = "active"
"$BIN" ps | grep -q "demo1"
"$BIN" ps | grep -q "running"
# up 后 --real 应展示磁盘上的实际文件内容
"$BIN" config --real demo1 | grep -q "MANAGED BY hr-compose"

echo "==> stop 保留 unit / start 恢复"
sudo "$BIN" stop
test "$(systemctl is-active smoke-demo-demo1.service)" = "inactive"
test -e /etc/systemd/system/smoke-demo-demo1.service
sudo "$BIN" start
test "$(systemctl is-active smoke-demo-demo1.service)" = "active"

echo "==> 指定服务（多名字）"
sudo "$BIN" stop demo1 demo2
test "$(systemctl is-active smoke-demo-demo1.service)" = "inactive"
sudo "$BIN" start demo1 demo2
test "$(systemctl is-active smoke-demo-demo1.service)" = "active"

echo "==> up 指定服务自动带上依赖"
sudo "$BIN" down >/dev/null 2>&1 || true
sudo "$BIN" up demo2   # demo2 依赖 demo1，应连 demo1 一并安装启动
test "$(systemctl is-active smoke-demo-demo1.service)" = "active"
test "$(systemctl is-active smoke-demo-demo2.service)" = "active"

echo "==> down 指定服务只清理指定的"
sudo "$BIN" down demo1
test ! -e /etc/systemd/system/smoke-demo-demo1.service
test -e /etc/systemd/system/smoke-demo-demo2.service
sudo "$BIN" up demo1 >/dev/null 2>&1 || true # 装回 demo1，便于最后 down 全部清理

echo "==> down 清理"
sudo "$BIN" down
test ! -e /etc/systemd/system/smoke-demo-demo1.service
test ! -e /etc/systemd/system/smoke-demo-demo2.service

echo "==> up 复核：command 不存在时不谎报成功"
# systemctl start 对 Type=simple 只要 fork 成功就返回 0，坏 command 会进入 auto-restart 循环。
# up 必须复核 systemd 状态：点名坏服务、转述 systemd 的失败口径（203/EXEC）、以非 0 退出，
# 同时不影响正常服务起来；状态表也不得把 [not set] 这类 systemd 内部字面量透出来。
mkdir -p bad && cd bad
cat > hr-compose.yml <<'YAML'
name: smoke-bad
services:
  good:
    command: /usr/bin/sleep infinity
  bad:
    command: /no/such/binary-at-all
YAML
rc=0
out="$(sudo "$BIN" up 2>&1)" || rc=$?
printf '%s\n' "$out" | sed 's/^/   /'
test "$rc" != 0
# 用 here-string 而非管道喂 grep：grep -q 命中即退出，管道上游可能吃到 SIGPIPE，
# 叠加 pipefail 会让"应当命中"的断言假失败。
grep -q 'bad' <<<"$out"
grep -q '203' <<<"$out"
grep -qE 'restarting|failed' <<<"$out"
! grep -qF '[not set]' <<<"$out"
test "$(systemctl is-active smoke-bad-good.service)" = "active"
sudo "$BIN" down >/dev/null 2>&1 || true
cd ..

echo "E2E OK"
