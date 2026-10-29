#!/usr/bin/env bash
# G. 决定性变体：由服务自己写入 300M 文件，让 page cache 归属到本服务 cgroup，
#    看 hr-compose ps 的 MEMORY 是否会远大于进程 RSS。顺带做主机遗留检查。
set -uo pipefail
BIN="${1:-/usr/local/bin/hr-compose}"
D=/tmp/pcprobe

echo "===== 0. 遗留检查（前面几轮测试是否清干净）====="
ls /etc/systemd/system/ | grep -E '^(memcheck|cfga|cycb|injc|injd|faild|rdfr|pcprobe)-' || echo "  无遗留测试 unit"
echo -n "  既有业务服务："; systemctl is-active hr-api.service hr-web.service 2>/dev/null | tr '\n' ' '; echo
systemctl list-units --no-pager --state=failed --plain 2>/dev/null | tail -3 | sed 's/^/  failed: /'

echo "===== G. 服务自写 300M 文件 ====="
rm -rf "$D"; mkdir -p "$D"
cat > "$D/w.sh" <<'EOS'
#!/bin/sh
dd if=/dev/zero of=/tmp/pcprobe/mine bs=1M count=300 status=none
exec /bin/sleep 45
EOS
cat > "$D/hr-compose.yml" <<EOF
name: pcprobe
services:
  writer:
    description: 服务自己写300M文件
    command: /bin/sh $D/w.sh
EOF
(cd "$D" && "$BIN" up writer >/dev/null 2>&1)
echo "  说明：VmRSS 是主进程常驻；file 是归属本 cgroup 的可回收页缓存"
el=0
for i in 1 2 3 4 5; do
  sleep 6; el=$((el + 6))
  mc=$(systemctl show pcprobe-writer.service -p MemoryCurrent --value)
  pid=$(systemctl show pcprobe-writer.service -p MainPID --value)
  rss=$(awk '/^VmRSS/{print $2}' "/proc/$pid/status" 2>/dev/null)
  st=/sys/fs/cgroup/system.slice/pcprobe-writer.service/memory.stat
  fl=$(awk '$1=="file"{print $2}' "$st" 2>/dev/null)
  an=$(awk '$1=="anon"{print $2}' "$st" 2>/dev/null)
  printf '  t+%2ss MemoryCurrent=%-10s file=%-9s anon=%-9s VmRSS=%s kB\n' "$el" "${mc:-?}" "${fl:-?}" "${an:-?}" "${rss:-?}"
done
echo "-- hr-compose ps 展示"
(cd "$D" && "$BIN" ps) 2>&1 | sed 's/^/  /'
echo "-- systemd status 展示"
systemctl status pcprobe-writer.service --no-pager | grep -E 'Memory:|Tasks:' | sed 's/^/  /'
echo "-- 主机整体内存（页缓存可回收）"
awk '/MemTotal|MemAvailable|Dirty/{print "  "$0}' /proc/meminfo

echo "===== 清理 ====="
(cd "$D" && "$BIN" down >/dev/null 2>&1 || true)
cd / && rm -rf "$D"
ls /etc/systemd/system/ | grep -E '^pcprobe-' || echo "  已清理干净"
