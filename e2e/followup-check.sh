#!/usr/bin/env bash
# 追加实证两件事（v0.4.0 真机）：
#   E. 跨段注入：description 里写入 "\n[Service]\nExecStartPre=..." 能否让 systemd 执行任意命令
#   F. page cache 膨胀：只读一次大文件后睡住，看 MemoryCurrent 是否远大于进程 RSS
set -uo pipefail
BIN="${1:-/usr/local/bin/hr-compose}"
WORK="$(mktemp -d)"; exec > >(tee "$WORK/out.log") 2>&1
say() { printf '\n===== %s =====\n' "$*"; }

d="$WORK/injd"; mkdir -p "$d"
# payload 用双引号标量把 \n 交给 YAML 解码，等价于用户手写块标量
cat > "$d/hr-compose.yml" <<EOF
name: injd
services:
  deep:
    description: "合法描述\n[Service]\nExecStartPre=/bin/touch $WORK/proof-E.txt\n"
    command: /bin/sleep 100000
EOF
say "E. 跨段注入"
(cd "$d" && "$BIN" config deep) 2>&1 | sed 's/^/   预览: /'
(cd "$d" && "$BIN" up deep) >/dev/null 2>&1; echo "   up 退出码=$?"
echo "-- 落盘 unit 全文"; sed 's/^/   /' "/etc/systemd/system/injd-deep.service"
echo "-- systemd 是否报错/警告（该 unit 的加载日志）"
journalctl -b --since "-3min" --no-pager 2>/dev/null | grep -iE "injd-deep|Unknown key|Ignoring|invalid" | tail -8 | sed 's/^/   /' || true
echo "-- unit 状态与注入指令是否执行"
systemctl show injd-deep.service -p LoadState -p ActiveState -p SubState -p ExecStartPre | sed 's/^/   /'
[ -e "$WORK/proof-E.txt" ] && echo "   !! 跨段注入成功：命令被执行了（proof-E.txt 存在）" || echo "   跨段注入未执行（systemd 忽略了该指令）"

r="$WORK/reader"; mkdir -p "$r"
dd if=/dev/zero of="$WORK/probe-file" bs=1M count=300 status=none
cat > "$r/readonce.sh" <<EOF
#!/bin/sh
dd if=$WORK/probe-file of=/dev/null bs=1M
exec /bin/sleep 60
EOF
cat > "$r/hr-compose.yml" <<EOF
name: rdfr
services:
  reader:
    description: 读一次300M文件后睡住
    command: /bin/sh $r/readonce.sh
EOF
say "F. page cache 膨胀"
(cd "$r" && "$BIN" up reader) >/dev/null 2>&1
el=0
for i in 1 2 3 4 5 6; do
  sleep 3; el=$((el + 3))
  mc="$(systemctl show rdfr-reader.service -p MemoryCurrent --value)"
  pid="$(systemctl show rdfr-reader.service -p MainPID --value)"
  rss="$(awk '/^VmRSS/{print $2}' "/proc/$pid/status" 2>/dev/null)"
  st="/sys/fs/cgroup/system.slice/rdfr-reader.service/memory.stat"
  fl="$(awk '$1=="file"{print $2}' "$st" 2>/dev/null)"
  an="$(awk '$1=="anon"{print $2}' "$st" 2>/dev/null)"
  printf '   t+%2ss  MemoryCurrent=%-11s file=%-10s anon=%-9s 主进程VmRSS=%s kB\n' \
    "$el" "${mc:-?}" "${fl:-?}" "${an:-?}" "${rss:-?}"
done
echo "-- 同一时刻 hr-compose ps 的展示（对比上面的数字）"
(cd "$r" && "$BIN" ps) 2>&1 | sed 's/^/   /'
echo "-- 主机当时可用内存（page cache 可回收，不是真占用）"; awk '/MemTotal|MemAvailable/{print "   "$0}' /proc/meminfo

echo "===== 清理 ====="
rm -f "$WORK/proof-E.txt"
(cd "$d" && $BIN down >/dev/null 2>&1 || true); (cd "$r" && $BIN down >/dev/null 2>&1 || true)
rm -rf "$WORK"
echo "   done"
