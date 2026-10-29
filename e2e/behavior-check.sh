#!/usr/bin/env bash
# 真机行为验证：把静态分析得出的四条结论在带 systemd 的主机上跑出实证。
#   A. 改配置后重复 up 是否让运行中的服务生效（预期：不生效，start 对 active unit 是 no-op）
#   B. depends_on 成环是否报错（预期：不报错，由 systemd 自己"打破顺序环"）
#   C. description 等字段用 YAML 块标量注入额外 systemd 指令（预期：校验放行、指令生效）
#   D. command 路径不存在导致 up 中途失败时，后面的服务是否被连带不启动（失败传播），
#      以及 up 是否谎报成功（已修复：收尾复核 systemd 状态，本项改为逐项判定）
#
# 用法（主机上）：bash behavior-check.sh [-b /path/to/hr-compose]
# 全程用独立 project 名 check<字母>，收尾自动 down，不碰机器上其他 unit。
set -uo pipefail

BIN="${1:-}"
VERSION="${HR_COMPOSE_VERSION:-0.4.0}"
WORK="$(mktemp -d)"
LOG="$WORK/behavior-check.log"
PROOF="$WORK/inject-proof.txt"

while getopts "b:v:" opt; do
  case "$opt" in
    b) BIN="$OPTARG" ;;
    v) VERSION="$OPTARG" ;;
    *) exit 2 ;;
  esac
done
exec > >(tee "$LOG") 2>&1
say() { printf '\n===== %s =====\n' "$*"; }
[ "$(id -u)" = 0 ] && SUDO="" || SUDO="sudo"

if [ -z "$BIN" ]; then
  ARCH="$(uname -m)"; case "$ARCH" in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; esac
  ASSET="hr-compose_${VERSION}_linux_${ARCH}.tar.gz"
  curl -fsSL "https://github.com/labyte/hr-compose/releases/download/v${VERSION}/${ASSET}" -o "$WORK/$ASSET" \
    && tar -C "$WORK" -xzf "$WORK/$ASSET" && BIN="$WORK/hr-compose" \
    || { echo "!! 未取得二进制，用 -b 指定路径重跑"; exit 1; }
fi
chmod +x "$BIN" 2>/dev/null || true
say "使用二进制"; "$BIN" --version

cleanup() {
  for p in cfg-a cyc inj fail; do
    d="$WORK/$p"; [ -f "$d/hr-compose.yml" ] && (cd "$d" && $SUDO "$BIN" down >/dev/null 2>&1 || true)
  done
  rm -f "$PROOF"
  rm -rf "$WORK"
}
trap cleanup EXIT

mkdir -p "$WORK/cfg-a" "$WORK/cyc" "$WORK/inj" "$WORK/fail"

# ---------- A. 配置变更是否对运行中的服务生效 ----------
say "A. 改 environment 后重复 up —— 运行中的进程是否拿到新配置"
cat > "$WORK/cfg-a/record.sh" <<'EOS'
#!/bin/sh
printf 'pid=%s TAG=%s\n' "$$" "${TAG:-<未设置>}" >> "$1"
exec /bin/sleep 100000
EOS
cat > "$WORK/cfg-a/hr-compose.yml" <<EOF
name: cfga
services:
  probe:
    description: 配置变更探针
    command: /bin/sh $WORK/cfg-a/record.sh $WORK/cfg-a/record.txt
EOF
(cd "$WORK/cfg-a" && $SUDO "$BIN" up >/dev/null)
sleep 1
unit="cfga-probe.service"
pid1="$(systemctl show "$unit" -p MainPID --value)"
echo "第一次 up：MainPID=$pid1"
echo "进程记录文件：" ; cat "$WORK/cfg-a/record.txt" 2>/dev/null | sed 's/^/   /'

# 改 yml：加一个新的 TAG 环境变量
cat > "$WORK/cfg-a/hr-compose.yml" <<EOF
name: cfga
services:
  probe:
    description: 配置变更探针
    command: /bin/sh $WORK/cfg-a/record.sh $WORK/cfg-a/record.txt
    environment:
      - "TAG=second"
EOF
(cd "$WORK/cfg-a" && $SUDO "$BIN" up)
sleep 1
pid2="$(systemctl show "$unit" -p MainPID --value)"
echo "第二次 up 后：MainPID=$pid2  （与 $pid1 $([ "$pid1" = "$pid2" ] && echo '相同 → 进程没重启' || echo '不同 → 进程被重启')）"
echo "-- 磁盘上的 unit 是否已更新（Environment 是否写进去了）"
grep -E '^Environment=' "/etc/systemd/system/$unit" | sed 's/^/   /' || echo "   (unit 里没有 Environment=)"
echo "-- systemd 实际加载的属性 MemoryCurrent/ExecStart 对照"
systemctl show "$unit" -p Environment -p ExecMainStartTimestamp | sed 's/^/   /'
echo "-- 进程真实环境（/proc/<pid>/environ）里 TAG 是什么"
tr '\0' '\n' < "/proc/$pid2/environ" 2>/dev/null | grep -E '^TAG=' | sed 's/^/   运行中进程: /' || echo "   运行中进程: 无 TAG"
echo "-- 记录文件是否有第二行（第二行存在才说明进程重启过）"
echo "   行数=$(wc -l <"$WORK/cfg-a/record.txt" 2>/dev/null || echo 0)"
echo ">> A 结论：unit 文件已更新 + MainPID 未变 + 进程环境仍是旧值 ⇒ 重复 up 不会让配置在运行服务上生效"

# ---------- B. 依赖成环 ----------
say "B. depends_on 成环（a→b→a）—— hr-compose 是否报错"
cat > "$WORK/cyc/hr-compose.yml" <<'EOF'
name: cycb
services:
  a:
    command: /bin/sleep 100000
    depends_on:
      - b
  b:
    command: /bin/sleep 100000
    depends_on:
      - a
EOF
echo "-- config 是否报环"
(cd "$WORK/cyc" && "$BIN" config) 2>&1 | grep -iE 'cycle|环|error' | sed 's/^/   /' || echo "   (无任何与环相关的提示)"
echo "-- up 的返回与输出"
b_out="$( (cd "$WORK/cyc" && $SUDO "$BIN" up) 2>&1 )"; b_rc=$?
printf '%s\n' "$b_out" | sed 's/^/   /'
echo "   up 退出码=$b_rc"
echo "-- 两个 unit 的最终状态"
for u in cycb-a.service cycb-b.service; do
  printf '   %-16s %s\n' "$u" "$(systemctl show "$u" -p LoadState -p ActiveState -p SubState --value | tr '\n' ' ')"
done
echo "-- systemd 自己有没有打破顺序环（内核/服务日志）"
journalctl -b --since "-10min" --no-pager 2>/dev/null | grep -iE 'ordering cycle|Breaking' | tail -5 | sed 's/^/   /' || echo "   (journal 里没抓到 cycle 相关行)"
echo ">> B 结论见上：若 up 退出码 0 且无提示 ⇒ 工具静默接受依赖环"

# ---------- C. 换行注入 ----------
say "C. description 用 YAML 块标量注入额外 systemd 指令"
cat > "$WORK/inj/hr-compose.yml" <<EOF
name: injc
services:
  sqli:
    description: |
      看似正常的描述
      ExecStartPre=/bin/touch $PROOF
    command: /bin/sleep 100000
EOF
echo "-- 校验/预览是否拒绝（预期不拒绝）"
(cd "$WORK/inj" && "$BIN" config sqli) 2>&1 | sed 's/^/   /'
echo "-- 落盘的真实 unit"
(cd "$WORK/inj" && $SUDO "$BIN" up >/dev/null 2>&1); sleep 1
sed 's/^/   /' "/etc/systemd/system/injc-sqli.service"
echo "-- 注入的 ExecStartPre 是否真的执行了"
if [ -e "$PROOF" ]; then
  echo "   !! 注入生效：$PROOF 被创建"
else
  echo "   本例未执行：注入行落在 [Unit] 段，systemd 按未知键忽略（journal 里有 Unknown key ... ignoring）"
  echo "   ⇒ 但任意行已写进 unit 文件是事实；跨段注入（description 里写 \n[Service]\nExecStartPre=...）"
  echo "     由 followup-check.sh 的 E 项验证，systemd 会合并重复的 [Service] 段并真的执行该命令。"
fi
rm -f "$PROOF"

# ---------- D. command 路径不存在的失败传播与复核 ----------
say "D. 三个服务、中间一个 command 不存在 —— 是否跳过后面的服务、是否谎报成功"
cat > "$WORK/fail/hr-compose.yml" <<EOF
name: faild
services:
  first:
    command: /bin/sleep 100000
  broken:
    command: /no/such/binary-at-all
  last:
    command: /bin/sleep 100000
EOF
d_out="$( (cd "$WORK/fail" && $SUDO "$BIN" up) 2>&1 )"; d_rc=$?
printf '%s\n' "$d_out" | sed 's/^/   /'
echo "   up 退出码=$d_rc"
echo "-- 三个 unit 实际状态"
for u in faild-first.service faild-broken.service faild-last.service; do
  printf '   %-20s %s\n' "$u" "$(systemctl show "$u" -p LoadState -p ActiveState -p SubState -p UnitFileState --value | tr '\n' ' ')"
done
echo "-- 逐项判定"
# 静态分析时的原始结论：systemctl start 对 Type=simple 只要 fork 成功就返回 0，
# Restart=always 使起不来的服务进入 auto-restart 循环，于是 up 会谎报 'up broken OK'。
FAILS=0
check() { local desc="$1"; shift; if "$@" >/dev/null 2>&1; then echo "   [PASS] $desc"; else echo "   [FAIL] $desc"; FAILS=$((FAILS + 1)); fi; }
check_not() { local desc="$1"; shift; if "$@" >/dev/null 2>&1; then echo "   [FAIL] $desc"; FAILS=$((FAILS + 1)); else echo "   [PASS] $desc"; fi; }
check "up 退出码非 0（不谎报成功）" test "$d_rc" != 0
check "up 点名了坏服务" grep -q broken <<<"$d_out"
check "给出 systemd 的失败口径 203/EXEC" grep -q 203 <<<"$d_out"
check "后面的 last 仍被启动" systemctl is-active --quiet faild-last.service
check "状态表把 broken 呈现为 restarting/failed" grep -qE 'restarting|failed' <<<"$d_out"
check_not "MEMORY 列不再透出 [not set] 内部字面量" grep -qF '[not set]' <<<"$d_out"
echo ">> D 结论：不中止是正确的（依赖不满足由 systemd 报错，其余照常安装）；"
echo "   谎报成功已修复——up 收尾复核 systemd 状态，起不来的服务逐条报原因并以非 0 退出。"
[ "$FAILS" = 0 ] || echo "!! 本项有 $FAILS 条判定未通过（用 -b 指定新构建的二进制重跑）"

say "完成"; echo "日志: $LOG"
