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
sudo "$BIN" up
test "$(systemctl is-active demo1.service)" = "active"
test "$(systemctl is-active demo2.service)" = "active"
"$BIN" ps | grep -q "demo1"
"$BIN" ps | grep -q "running"
# up 后 --real 应展示磁盘上的实际文件内容
"$BIN" config --real demo1 | grep -q "MANAGED BY hr-compose"

echo "==> stop 保留 unit / start 恢复"
sudo "$BIN" stop
test "$(systemctl is-active demo1.service)" = "inactive"
test -e /etc/systemd/system/demo1.service
sudo "$BIN" start
test "$(systemctl is-active demo1.service)" = "active"

echo "==> down 清理"
sudo "$BIN" down
test ! -e /etc/systemd/system/demo1.service
test ! -e /etc/systemd/system/demo2.service

echo "E2E OK"
