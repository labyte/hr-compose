#!/usr/bin/env bash
# 真机内存统计准确性实测：在带 systemd 的 Linux 主机上跑三个可控服务，
# 把 hr-compose ps 的 MEMORY 列与 systemctl / cgroup 文件 / /proc 的读数横向对比，
# 判断口径差异（page cache、多进程、swap、未安装 unit 的展示）。
#
# 用法（在主机上）：
#   bash mem-check.sh                        # 自动下载 v0.4.0 发行包
#   bash mem-check.sh -b /path/to/hr-compose # 用指定二进制（改成自编译版本时用它）
#   HR_COMPOSE_VERSION=0.4.0 bash mem-check.sh
#
# 输出：全部结果同时写到 stdout 与 $WORK/mem-check.log（把 log 拷回来分析即可）。
set -uo pipefail

BIN=""
VERSION="${HR_COMPOSE_VERSION:-0.4.0}"
WORK="${WORK_DIR:-$(mktemp -d)}"
PROJECT="memcheck"
LOG="$WORK/mem-check.log"

while getopts "b:v:p:" opt; do
  case "$opt" in
    b) BIN="$OPTARG" ;;
    v) VERSION="$OPTARG" ;;
    p) PROJECT="$OPTARG" ;;
    *) exit 2 ;;
  esac
done

exec > >(tee "$LOG") 2>&1
say() { printf '\n===== %s =====\n' "$*"; }

[ "$(id -u)" = 0 ] && SUDO="" || SUDO="sudo"

say "0. 主机环境"
uname -srm
. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME"
echo "systemd: $(systemctl --version | head -1)"
echo "cgroup 层级: $(stat -fc %T /sys/fs/cgroup 2>/dev/null)"
echo "内核内存: $(awk '/MemTotal/{printf "%.1fG", $2/1024/1024}' /proc/meminfo)  CPU: $(nproc)"
echo "DefaultMemoryAccounting: $(systemctl show --property=DefaultMemoryAccounting 2>/dev/null || echo '(属性不存在)')"
CGV2=0
[ "$(stat -fc %T /sys/fs/cgroup 2>/dev/null)" = "cgroup2fs" ] && CGV2=1

# --- 定位或下载二进制 ---
if [ -z "$BIN" ]; then
  ARCH="$(uname -m)"; case "$ARCH" in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; esac
  ASSET="hr-compose_${VERSION}_linux_${ARCH}.tar.gz"
  URL="https://github.com/labyte/hr-compose/releases/download/v${VERSION}/${ASSET}"
  echo "==> 下载发行包: $URL"
  if curl -fsSL "$URL" -o "$WORK/$ASSET" && tar -C "$WORK" -xzf "$WORK/$ASSET"; then
    BIN="$WORK/hr-compose"
  else
    echo "!! 下载失败（主机到 GitHub 不通？）——请把二进制放到主机上后用 -b 指定路径重跑"
    exit 1
  fi
fi
chmod +x "$BIN" 2>/dev/null || true
say "使用二进制"
"$BIN" --version || true

# --- 造三个可控服务 ---
mkdir -p "$WORK/app"
cat > "$WORK/app/idle.sh" <<'EOS'
#!/bin/sh
exec /bin/sleep 100000
EOS
cat > "$WORK/app/anon.sh" <<'EOS'
#!/bin/sh
# 常驻 128MB 匿名内存，不碰文件；RSS 与 cgroup 值应当基本一致
exec python3 -c 'b = bytearray(128 * 1024 * 1024); import time; time.sleep(100000)'
EOS
cat > "$WORK/app/pagecache.sh" <<'EOS'
#!/bin/sh
# 反复读一个 400MB 文件：匿名内存几乎不变，但读进来的 page cache 会计入本服务 cgroup。
# 每轮 sleep 2 避免把小规格主机的 CPU 打满（缓存留在 cgroup 里不会掉）。
FILE="$1"
while :; do cat "$FILE" >/dev/null; sleep 2; done
EOS
chmod +x "$WORK/app"/*.sh
dd if=/dev/zero of="$WORK/bigfile" bs=1M count=400 status=none
command -v python3 >/dev/null || echo "!! 本机无 python3，anon 服务将起不来（改用 stress 或忽略该项）"

cat > "$WORK/hr-compose.yml" <<EOF
name: $PROJECT
services:
  idle:
    description: 基线-几乎不占内存
    command: /bin/sh $WORK/app/idle.sh
  anon:
    description: 常驻128M匿名内存
    command: /bin/sh $WORK/app/anon.sh
  cache:
    description: 反复读400M文件制造page cache
    command: /bin/sh $WORK/app/pagecache.sh $WORK/bigfile
  capped:
    description: 带 memory_max/cpu_quota 兼容性探针
    command: /bin/sh $WORK/app/idle.sh
    memory_max: 300M
    cpu_quota: 50%
EOF

say "1. 生成的 unit（确认 MemoryMax/CPUQuota 是否被本 systemd 版本支持）"
(cd "$WORK" && "$BIN" config capped)

say "2. up（若 MemoryMax 在本 systemd 上是未知指令，这里或下一步会暴露）"
(cd "$WORK" && $SUDO "$BIN" up) || echo "!! up 失败，见上方错误"

trap '(cd "$WORK" && $SUDO "$BIN" down >/dev/null 2>&1 || true); rm -rf "$WORK"' EXIT

say "3. hr-compose ps 原始输出（cat -A 显示空白单元格与制表符）"
(cd "$WORK" && "$BIN" ps)
say "3b. 同上但显式标注行尾（排查 MEMORY/PID 空值显示成空白还是 -）"
(cd "$WORK" && "$BIN" ps | sed -n 's/$/|/p' | cat -A | head -20)

unit() { echo "${PROJECT}-${1}.service"; }

# 逐个服务把 5 个口径的数拉出来横向对比
for svc in idle anon cache capped; do
  u="$(unit "$svc")"
  say "4. $svc → $u"
  echo "-- systemctl show 关键属性"
  systemctl show "$u" -p LoadState -p ActiveState -p SubState -p MainPID \
    -p MemoryCurrent -p MemoryAccounting -p MemoryMax -p MemorySwapCurrent -p CPUQuota | sed 's/^/   /'
  echo "-- 本进程 RSS（/proc/<MainPID>/status，单进程口径）"
  pid="$(systemctl show "$u" -p MainPID --value)"
  if [ -n "${pid:-}" ] && [ "$pid" != 0 ] && [ -r "/proc/$pid/status" ]; then
    awk '/^VmRSS|^RssAnon|^RssFile/{print "   " $0}' "/proc/$pid/status"
    grep -E '^(Rss|Pss|Anonymous|Shared|Dirty):' "/proc/$pid/smaps_rollup" 2>/dev/null | sed 's/^/   rollup /'
  else
    echo "   (MainPID 不可用)"
  fi
  echo "-- cgroup 计数（unit 全体的口径）"
  if [ "$CGV2" = 1 ]; then
    d="/sys/fs/cgroup/system.slice/$u"
    [ -r "$d/memory.current" ] && echo "   memory.current = $(cat "$d/memory.current")"
    [ -r "$d/memory.stat" ] && grep -E '^(anon|file|kernel|slab|pagetables|workingset|inactive|active) ' "$d/memory.stat" | sed 's/^/   /'
    [ -r "$d/memory.swap.current" ] && echo "   memory.swap.current = $(cat "$d/memory.swap.current")"
    [ -r "$d/cpu.stat" ] && echo "   cpu.stat 存在(v2无 usage 用 cpuacct 见下)"
    [ -r "$d/cpuacct.usage" ] && echo "   cpuacct.usage = $(cat "$d/cpuacct.usage")"
  else
    d="/sys/fs/cgroup/memory/system.slice/$u"
    [ -r "$d/memory.usage_in_bytes" ] && echo "   memory.usage_in_bytes = $(cat "$d/memory.usage_in_bytes")"
    [ -r "$d/memory.stat" ] && grep -E '^(rss|cache|total_rss|total_cache|mapped_file|inactive_anon|active_anon|inactive_file|active_file)( |\n)' "$d/memory.stat" | sed 's/^/   /'
    [ -r "$d/memory.max_usage_in_bytes" ] && echo "   memory.max_usage_in_bytes = $(cat "$d/memory.max_usage_in_bytes")"
  fi
  echo "-- unit 内所有进程 RSS 之和"
  cs="/sys/fs/cgroup/system.slice/$u/cgroup.procs"
  [ "$CGV2" = 1 ] || cs="/sys/fs/cgroup/memory/system.slice/$u/cgroup.procs"
  if [ -r "$cs" ]; then
    total=0
    for p in $(cat "$cs"); do
      r="$(awk '/^VmRSS:/{print $2}' "/proc/$p/status" 2>/dev/null)"
      [ -n "$r" ] && total=$((total + r))
    done
    echo "   tasks=$(wc -w <"$cs") sum(VmRSS)=${total} kB"
  else
    echo "   (读不到 cgroup.procs)"
  fi
  echo "-- systemctl status 显示的 Memory（systemd 自己的展示口径）"
  systemctl status "$u" --no-pager 2>/dev/null | grep -E 'Memory:|Tasks:' | sed 's/^/   /'
done

say "5. swap 影响验证：把 cache 服务的一部分压出去（需要主机有 swap）"
if swapon --show 2>/dev/null | grep -q .; then
  echo "-- 有 swap：MemoryCurrent 不含 swap 部分，MemorySwapCurrent 单独统计"
  for svc in idle anon cache capped; do
    u="$(unit "$svc")"
    printf '   %-8s Mem=%s Swap=%s\n' "$svc" \
      "$(systemctl show "$u" -p MemoryCurrent --value)" \
      "$(systemctl show "$u" -p MemorySwapCurrent --value)"
  done
else
  echo "   (主机未启用 swap，跳过)"
fi

say "6. down 之后（未安装 unit 的展示：应为 not-found，MEMORY/PID 列注意空白 vs -）"
(cd "$WORK" && $SUDO "$BIN" down >/dev/null)
(cd "$WORK" && "$BIN" ps | sed -n 's/$/|/p' | cat -A | head -20)
echo "-- systemctl 对未安装 unit 的原始返回"
systemctl show "$(unit idle)" -p MemoryCurrent -p LoadState -p MainPID | sed 's/^/   /'
systemctl show "$(unit idle)" -p MemoryCurrent -p LoadState -p MainPID --value | sed 's/^/   value形式: /'

say "完成"
echo "日志文件: $LOG"
