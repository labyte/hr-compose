#!/usr/bin/env bash
# 口径对比 + 页缓存归属实证（避免用 tmpfs：tmpfs 的页不可回收，不是"页缓存"）。
#   A 现口径   MemoryCurrent = cgroup memory.current（anon + file + kernel + slab）
#   B anon    memory.stat anon（不可回收，OOM 真正的驱动者）
#   C workingset = memory.current − inactive_file（Kubernetes 的 working set 定义）
#   D 主进程 RSS   E 全任务 RSS 之和
set -uo pipefail
BIN="${1:-/usr/local/bin/hr-compose}"

# --- 选一个磁盘-backed 目录放测试文件（避开 tmpfs）---
pick_dir() {
  for p in /var/tmp /srv /opt /root /home /var; do
    [ -d "$p" ] && [ -w "$p" ] || continue
    fs="$(findmnt -T "$p" -n -o FSTYPE 2>/dev/null | head -1)"
    case "$fs" in tmpfs|"") ;; *) echo "$p|$fs"; return ;; esac
  done
  echo "NONE"
}
picked="$(pick_dir)"
if [ "$picked" = NONE ]; then echo "!! 找不到磁盘-backed 可写目录（全是 tmpfs？）"; exit 1; fi
BASE="${picked%%|*}"; FSTYPE="${picked##*|}"
D="$BASE/hr-memf"; rm -rf "$D"; mkdir -p "$D"

echo "===== 环境 ====="
for p in /tmp "$BASE"; do printf '  %-10s fstype=%s\n' "$p" "$(findmnt -T "$p" -n -o FSTYPE 2>/dev/null | head -1)"; done
echo "  测试文件将放在 $D（$FSTYPE，非 tmpfs ⇒ 读写的页才是真正的可回收页缓存）"
echo "  up 之前 MemAvailable: $(awk '/MemAvailable/{print $2/1024 "M"}' /proc/meminfo)"

cat > "$D/w.sh" <<EOS
#!/bin/sh
dd if=/dev/zero of=$D/mine bs=1M count=300 status=none
exec /bin/sleep 300
EOS
cat > "$D/r.sh" <<EOS
#!/bin/sh
# 只读 writer 已经落盘的文件：cgroup v2 下页缓存归属可能记到先触碰的 cgroup
while :; do cat $D/mine >/dev/null; sleep 3; done
EOS
cat > "$D/child.sh" <<EOS
#!/bin/sh
python3 -c 'b = bytearray(64*1024*1024); import time; time.sleep(300)' &
exec /bin/sleep 300
EOS
cat > "$D/hr-compose.yml" <<EOF
name: memf
services:
  writer:
    description: 写300M到磁盘文件
    command: /bin/sh $D/w.sh
  reader:
    description: 反复读那300M磁盘文件
    command: /bin/sh $D/r.sh
    depends_on:
      - writer
  child:
    description: 64M内存在子进程里
    command: /bin/sh $D/child.sh
EOF

(cd "$D" && "$BIN" up >/dev/null 2>&1)
echo "  等待写入与读循环稳定（25s）..."
sleep 25
echo "  up 之后 MemAvailable: $(awk '/MemAvailable/{print $2/1024 "M"}' /proc/meminfo)"

mb() { awk -v b="${1:-0}" 'BEGIN{printf "%.1fM", b/1048576}'; }

for svc in writer reader child; do
  u="memf-${svc}.service"
  cg="/sys/fs/cgroup/system.slice/${u}"
  cur="$(systemctl show "$u" -p MemoryCurrent --value)"
  pid="$(systemctl show "$u" -p MainPID --value)"
  tasks="$(wc -w <"$cg/cgroup.procs" 2>/dev/null || echo 0)"
  sumrss=0
  for p in $(cat "$cg/cgroup.procs" 2>/dev/null); do
    r="$(awk '/^VmRSS/{print $2}' "/proc/$p/status" 2>/dev/null)"; [ -n "$r" ] && sumrss=$((sumrss + r * 1024))
  done
  mainrss="$(awk '/^VmRSS/{print $2*1024}' "/proc/$pid/status" 2>/dev/null || echo 0)"
  mainpss="$(awk '/^Pss:/{s+=$2} END{print s*1024}' "/proc/$pid/smaps_rollup" 2>/dev/null || echo 0)"
  val() { awk -v k="$2" '$1==k{print $2+0} ' "$1/memory.stat" 2>/dev/null | head -1; }
  anon="$(val "$cg" anon)"; file="$(val "$cg" file)"; inact="$(val "$cg" inactive_file)"
  unev="$(val "$cg" unevictable)"; slab="$(val "$cg" slab)"
  echo
  echo "===== $svc（任务数=$tasks，MainPID=$pid）====="
  printf '  A MemoryCurrent = %-9s  B anon = %-9s  C current−inactive_file = %s\n' \
    "$(mb "$cur")" "$(mb "$anon")" "$(mb $(( ${cur:-0} - ${inact:-0} )) )"
  printf '  D 主进程RSS = %-9s  E 全任务RSS和 = %-9s  主进程PSS = %s\n' "$(mb "$mainrss")" "$(mb "$sumrss")" "$(mb "$mainpss")"
  printf '  memory.stat 关键行：file=%s anon=%s inactive_file=%s unevictable=%s slab=%s\n' \
    "$(mb "$file")" "$(mb "$anon")" "$(mb "$inact")" "$(mb "$unev")" "$(mb "$slab")"
  printf '  systemd status: %s\n' "$(systemctl status "$u" --no-pager 2>/dev/null | awk '/Memory:/{gsub(/^ +/,""); print; exit}')"
done

echo
echo "===== hr-compose ps 展示 ====="
(cd "$D" && "$BIN" ps) 2>&1 | sed 's/^/  /'

echo "===== 清理 ====="
(cd "$D" && "$BIN" down >/dev/null 2>&1 || true)
cd / && rm -rf "$D"
ls /etc/systemd/system/ | grep -E '^memf-' || echo "  已清理干净"
