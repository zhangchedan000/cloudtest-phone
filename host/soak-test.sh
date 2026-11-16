#!/usr/bin/env bash
# 长时间稳定性测试（只读，不改手机）：每隔一段时间记录每台运行中手机和宿主机的状态，结束时出小结。
# 用法（root，在仓库目录）：
#   bash host/soak-test.sh [小时数，默认 24] [间隔分钟，默认 10]
# 建议用 systemd-run 跑，不受 SSH 断开影响：
#   systemd-run --unit=pp-soak --collect bash /opt/prism-phone/repo/host/soak-test.sh 24 10
# 结果：/opt/prism-phone/soak/<开始时间>/log.tsv（每行一台手机一次采样）和 summary.txt。
set -uo pipefail
hours=${1:-24}; every=${2:-10}
out=/opt/prism-phone/soak/$(date +%Y%m%d-%H%M); mkdir -p "$out"
T=$(sed -n 's/^PP_TOKEN=//p' /etc/prism-phone/agent.env)
api() { curl -s -m 30 "http://127.0.0.1:8099/api$1" -H "Authorization: Bearer $T"; }
log="$out/log.tsv"
printf 'time\tphone\tstatus\tboot\twifi\tnet204\tmemMB\tcpu\tcrashes\tuptime_s\thost_avail_mb\tload1\tagent_restarts\n' > "$log"
restarts0=$(systemctl show prism-phone-agent -p NRestarts --value 2>/dev/null || echo 0)
pid0=$(systemctl show prism-phone-agent -p MainPID --value)
agent_starts=0
end=$(( $(date +%s) + hours*3600 ))
echo "开始：$(date)，共 $hours 小时，每 $every 分钟一次，结果在 $out" | tee "$out/summary.txt"
while [ "$(date +%s)" -lt "$end" ]; do
  now=$(date +%FT%T)
  avail=$(awk '/MemAvailable/{print int($2/1024)}' /proc/meminfo)
  load=$(cut -d' ' -f1 /proc/loadavg)
  pid=$(systemctl show prism-phone-agent -p MainPID --value)
  [ "$pid" != "$pid0" ] && { agent_starts=$((agent_starts+1)); pid0=$pid; }
  api /phones | python3 -c '
import json,sys
for p in json.load(sys.stdin):
    if p.get("status")!="stopped":
        u=p.get("usage") or {}
        print(p["id"], p.get("status"), round(u.get("memMB",0)), round(u.get("cpuPercent",0),1))
' 2>/dev/null | while read -r id st mem cpu; do
    C=pp-$id
    boot=$(docker exec "$C" getprop sys.boot_completed 2>/dev/null)
    wifi=$(docker exec "$C" cmd wifi status 2>/dev/null | grep -c "connected to")
    w=$(docker exec "$C" sh -c '(printf "GET /generate_204 HTTP/1.1\r\nHost: connectivitycheck.gstatic.com\r\nConnection: close\r\n\r\n"; sleep 8) | nc connectivitycheck.gstatic.com 80 | head -1' 2>/dev/null)
    case "$w" in *" 204"*) n=1;; *) n=0;; esac
    cr=$(docker exec "$C" sh -c 'logcat -d -b crash 2>/dev/null | grep -c "FATAL EXCEPTION\|Fatal signal"' 2>/dev/null)
    up=$(docker exec "$C" cut -d. -f1 /proc/uptime 2>/dev/null)
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$now" "$id" "$st" "${boot:-?}" "$wifi" "$n" "$mem" "$cpu" "${cr:-?}" "${up:-?}" "$avail" "$load" "$agent_starts" >> "$log"
  done
  sleep $((every*60))
done
{
  echo "结束：$(date)"
  echo "管家程序重启次数（含自动更新）：$agent_starts（systemd 记录的异常重启：$(( $(systemctl show prism-phone-agent -p NRestarts --value) - restarts0 ))）"
  python3 - "$log" <<'PY'
import csv,sys
rows=list(csv.DictReader(open(sys.argv[1]),delimiter='\t'))
by={}
for r in rows: by.setdefault(r['phone'],[]).append(r)
for pid,rs in by.items():
    n=len(rs)
    bad_net=sum(r['net204']!='1' for r in rs); bad_wifi=sum(r['wifi']!='1' for r in rs)
    not_running=sum(r['status']!='running' or r['boot']!='1' for r in rs)
    mems=[int(r['memMB']) for r in rs if r['memMB'].isdigit()]
    ups=[int(r['uptime_s']) for r in rs if r['uptime_s'].isdigit()]
    reboots=sum(1 for a,b in zip(ups,ups[1:]) if b<a)
    crashes=[int(r['crashes']) for r in rs if r['crashes'].isdigit()]
    print(f"手机 {pid}：采样 {n} 次；不在运行 {not_running}；WiFi 没连 {bad_wifi}；上不了网 {bad_net}；"
          f"中途重启 {reboots}；内存 {min(mems) if mems else '?'}–{max(mems) if mems else '?'} MB；"
          f"崩溃记录 {crashes[0] if crashes else '?'}→{crashes[-1] if crashes else '?'}")
av=[int(r['host_avail_mb']) for r in rows if r['host_avail_mb'].isdigit()]
if av: print(f"宿主机可用内存：最低 {min(av)} MB，最高 {max(av)} MB")
PY
} | tee -a "$out/summary.txt"
