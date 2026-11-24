#!/bin/sh
# 多开 + ADB 拉值：同时开 N 台（安卓 14/15/16 普通 / 谷歌混着、挂不同国家的代理），开满后：
#   1) 用 ADB（和用户电脑连手机同一条路：adb connect <手机IP>:5555，shell 身份）把各项值读出来；
#   2) 查唯一性、跟代理国家是否对得上（时区 / 语言 / SIM / 运营商）、容器痕迹；
#   3) 同时开 N 路实时画面 30 秒，看 CPU / 温度 / 每路码流；
#   4) 删净（KEEP=1 全留着；KEEP_BAD=1 只留有问题的，方便查）。在板子上 root 跑。
#
# 用法：sh host/test/multi.sh [规格 ...]
#   规格 = <版本>[g]:<国家代码 或 direct>[@机型]，g = 谷歌版。不写机型就在官方系统有这个版本的机型里轮流挑
#   （POCO X4 GT 官方只到安卓 14，配 15/16 面板会提示「没有这个版本」，测试也别这么配）。不给就用默认 8 台：
#   14:JP 14g:HK 15:SG 15g:US 16:TW 16g:KR 16:JP 15:direct
# 结果：/mmc/pp-build/multi-<时间>/（每台一个 .kv 原始值 + report.txt 汇总）
set -u
# 手机开了 SELinux：命令交给手机里的小助手跑（宿主机 docker exec 进去的命令，安卓的服务管家不认，查电话 / 蓝牙 / 软件包
# 全会失败，踩坑 #135）；没开的照旧 docker exec
PP_AGENT=${PP_AGENT:-/opt/prism-phone/prism-phone-agent}
PP_DATA=${PP_DATA:-/mmc/pp-data}
pex() { _c=$1; shift; _s=$PP_DATA/phones/${_c#pp-}/ppc/s
  if [ -S "$_s" ]; then "$PP_AGENT" pp-cmdc "$_s" -- "$@" </dev/null; else docker exec "$_c" "$@" </dev/null; fi; }
# 手机里的崩溃记录：崩溃缓冲区 + 每个 tombstone 的进程名、信号、调用栈开头（手机删了就没了）
crashdump() { pex "pp-$1" sh -c 'echo "== logcat -b crash"; logcat -d -b crash | tail -150; for t in $(ls -t /data/tombstones/tombstone_* 2>/dev/null | grep -v pb | head -6); do echo "== $t"; grep -m1 "^Cmdline" $t; grep -m1 "^signal" $t; grep -m1 "^Abort message" $t; sed -n "/^backtrace:/,/^$/p" $t | head -25; done'; }
API=http://127.0.0.1:8099/api
TOK=${PP_TOKEN:-$(sed -n 's/^PP_TOKEN="\(.*\)"$/\1/p' /etc/conf.d/prism-phone-agent 2>/dev/null)}
[ -n "$TOK" ] || { echo "拿不到口令"; exit 1; }
H="Authorization: Bearer $TOK"
# REUSE=<上次的结果目录>：不新建手机，直接对那批还开着的手机拉值 / 测画面（配合 KEEP=1 用）
DIR=${REUSE:-/mmc/pp-build/multi-$(date +%m%d-%H%M)}; mkdir -p "$DIR"
log(){ echo "$(date +%H:%M:%S) $*" | tee -a "$DIR/run.log"; }
SPECS=${*:-"14:JP 14g:HK 15:SG 15g:US 16:TW 16g:KR 16:JP 15:direct"}

command -v adb >/dev/null || apk add -q android-tools || { echo "装不上 adb（apk add android-tools）"; exit 1; }
export HOME=/mmc/pp-build/adbhome; mkdir -p "$HOME"   # adb 的钥匙放大盘
adb start-server >/dev/null 2>&1
KEY=$(cat "$HOME/.android/adbkey.pub")

# 国家代码 → 一个检测通过的代理 ID
curl -s -H "$H" "$API/proxies" > "$DIR/proxies.json"
proxy_of(){
  [ "$1" = direct ] && { echo direct-dev; return; }
  python3 -c 'import sys,json
d=json.load(open(sys.argv[1])); d=d.get("proxies",d) if isinstance(d,dict) else d
for p in d:
    c=p.get("check") or p.get("lastCheck") or {}
    if c.get("ok") and (c.get("country") or c.get("countryCode"))==sys.argv[2]: print(p["id"]); break' "$DIR/proxies.json" "$1"
}

curl -s -H "$H" "$API/models" > "$DIR/models.json"
# 第 n 台、安卓 ver → 官方系统有这个版本的机型（轮流挑，多测几款）
model_of(){ python3 -c 'import sys,json
d=json.load(open(sys.argv[1])); d=d.get("models",d) if isinstance(d,dict) else d
ok=[m["id"] for m in d if int(sys.argv[2]) in (m.get("androidVersions") or [])]
print(ok[int(sys.argv[3]) % len(ok)] if ok else "")' "$DIR/models.json" "$1" "$2"; }

if [ -z "${REUSE:-}" ]; then
# ---- 建 ----
: > "$DIR/phones.txt"
n=0
for s in $SPECS; do
  n=$((n+1)); ver=${s%%:*}; cc=${s#*:}; g=false; mdl=
  case $cc in *@*) mdl=${cc#*@}; cc=${cc%%@*} ;; esac
  case $ver in *g) g=true; ver=${ver%g} ;; esac
  [ -n "$mdl" ] || mdl=$(model_of "$ver" "$(( n + ${MODEL_OFFSET:-$(date +%M)} ))")
  [ -n "$mdl" ] || { log "没有官方系统是安卓 $ver 的机型，跳过 $s"; continue; }
  px=$(proxy_of "$cc"); [ -n "$px" ] || { log "没有 $cc 的可用代理，跳过 $s"; continue; }
  body="{\"name\":\"multi-$n-$ver$( [ $g = true ] && echo g)-$cc\",\"modelId\":\"$mdl\",\"androidVersion\":$ver,\"proxyId\":\"$px\",\"gapps\":$g}"
  id=$(curl -s -m20 -X POST -H "$H" -H 'Content-Type: application/json' -d "$body" "$API/phones" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id",""))' 2>/dev/null)
  [ -n "$id" ] && echo "$id $ver $g $cc $px $mdl" >> "$DIR/phones.txt" || log "建失败：$s"
done
log "建了 $(wc -l < "$DIR/phones.txt") 台：$SPECS"

# ---- 同时开机 ----
t0=$(date +%s)
while read -r id _; do
  ( r=$(curl -s -m300 -w '\n%{http_code}' -X POST -H "$H" "$API/phones/$id/start"); c=$(echo "$r" | tail -1)
    case $c in 2*) ;; *) log "  $id 开机请求失败：$(echo "$r" | head -c 200 | tr '\n' ' ')" ;; esac ) &
done < "$DIR/phones.txt"
wait
log "开机请求都发了（$(( $(date +%s) - t0 )) 秒），等开机完成"
: > "$DIR/boot.txt"
for _ in $(seq 1 90); do
  left=0
  while read -r id _; do
    grep -q "^$id " "$DIR/boot.txt" && continue
    [ "$(docker inspect -f '{{.State.Running}}' "pp-$id" 2>/dev/null)" = true ] || continue   # 没开起来的（比如内存把关拒了）不等
    if [ "$(pex "pp-$id" getprop sys.boot_completed 2>/dev/null)" = 1 ]; then echo "$id $(( $(date +%s) - t0 ))" >> "$DIR/boot.txt"; else left=$((left+1)); fi
  done < "$DIR/phones.txt"
  [ "$left" = 0 ] && break
  sleep 5
done
log "开机完成 $(wc -l < "$DIR/boot.txt")/$(wc -l < "$DIR/phones.txt")；等 60 秒（开机后设置、注册网络、蓝牙）"
sleep 60
fi

# ---- 宿主机负载（全开着、没看画面时） ----
{
  echo "== 空闲（$(wc -l < "$DIR/phones.txt") 台开着）"
  echo "load: $(cut -d' ' -f1-3 /proc/loadavg)  温度: $(( $(cat /sys/class/thermal/thermal_zone0/temp)/1000 ))℃"
  free -m | sed -n 1,2p
  docker stats --no-stream --format '{{.Name}} cpu={{.CPUPerc}} mem={{.MemUsage}}' | grep '^pp-'
} > "$DIR/host-idle.txt"

# ---- ADB 拉值（shell 身份，和用户电脑上 adb shell 看到的一样） ----
PULL='
for k in ro.product.model ro.product.brand ro.product.manufacturer ro.product.device ro.product.name \
  ro.build.version.release ro.build.version.sdk ro.build.fingerprint ro.build.version.incremental ro.vendor.build.fingerprint ro.build.type ro.build.tags ro.serialno \
  ro.hardware ro.board.platform ro.kernel.qemu ro.boot.qemu ro.debuggable ro.secure ro.adb.secure \
  persist.sys.timezone persist.sys.locale ro.product.locale gsm.sim.state gsm.operator.alpha gsm.sim.operator.alpha \
  gsm.operator.iso-country gsm.sim.operator.iso-country gsm.network.type net.dns1; do echo "$k=$(getprop $k)"; done
echo "android_id=$(settings get secure android_id)"
echo "bt_addr=$(settings get secure bluetooth_address)"
echo "bt_name=$(settings get secure bluetooth_name)"
echo "bt_state=$(dumpsys bluetooth_manager 2>/dev/null | grep -m1 -E "^ *state:" | tr -d " " | cut -d: -f2)"
echo "imei_raw=$(service call iphonesubinfo 1 s16 com.android.shell 2>&1 | tr -d "\n")"
echo "wifi_mac=$(cat /sys/class/net/wlan0/address 2>&1)"
echo "ifaces=$(ls /sys/class/net 2>&1 | tr "\n" " ")"
echo "wifi_ssid=$(dumpsys wifi 2>/dev/null | grep -m1 -oE "SSID: [^,]*")"
echo "wm_size=$(wm size | tail -1)"; echo "wm_density=$(wm density | tail -1)"
echo "battery=$(dumpsys battery | grep -E " level| status| temperature" | tr -s " " | tr "\n" " ")"
echo "gles=$(dumpsys SurfaceFlinger 2>/dev/null | grep -m1 "GLES: ")"
echo "cpu_hw=$(grep -m1 -i hardware /proc/cpuinfo)"; echo "nproc=$(nproc)"; echo "memtotal=$(grep MemTotal /proc/meminfo)"
echo "uname=$(uname -r)"; echo "uptime=$(cut -d" " -f1 /proc/uptime)"; echo "boot_id=$(cat /proc/sys/kernel/random/boot_id)"
echo "tellprops=$(getprop | grep -ciE "redroid|goldfish|ranchu|qemu|vbox|docker|cuttlefish")"
echo "tellprops_list=$(getprop | grep -iE "redroid|goldfish|ranchu|qemu|vbox|docker|cuttlefish" | head -6 | tr "\n" ";")"
echo "mount_tells=$(grep -ciE "docker|overlay|redroid" /proc/mounts)"
echo "dev_tells=$(ls /dev 2>/dev/null | grep -ciE "goldfish|qemu|vbox|loop[0-9]")"
echo "sensors=$(dumpsys sensorservice 2>/dev/null | grep -icE "android.sensor")"
echo "uid=$(id -u)"
echo "pkgs=$(pm list packages 2>/dev/null | wc -l)"; echo "gms=$(pm list packages com.google.android.gms 2>/dev/null | wc -l)"
echo "tz_now=$(date +%Z%z)"
echo "ping=$(ping -c1 -W3 1.1.1.1 >/dev/null 2>&1 && echo ok || echo fail)"
echo "vk_device=$(cmd gpu vkjson 2>/dev/null | grep -m1 deviceName | tr -d " \t,\"" | cut -d: -f2)"
echo "vk_drivers=$(ls /vendor/lib64/hw 2>/dev/null | grep "^vulkan" | tr "\n" " ")"
echo "sensor_vendors=$(dumpsys sensorservice 2>/dev/null | grep -E "^0x0000" | cut -d"|" -f2 | tr -s " " | sort -u | tr "\n" ";")"
echo "features_n=$(pm list features 2>/dev/null | wc -l)"; echo "feat_ethernet=$(pm list features 2>/dev/null | grep -c android.hardware.ethernet)"
echo "codec_rk_alias=$(dumpsys media.player 2>/dev/null | grep -oE "(c2|OMX)\.[A-Za-z0-9_.-]+" | grep -vE "^(c2\.android|OMX\.google|c2\.rk)\." | sort -u | wc -l)"
echo "data_kb=$(df -k /data 2>/dev/null | tail -1 | tr -s " " | cut -d" " -f2)"
C=/sys/devices/system/cpu
echo "cpu_parts=$(grep "CPU part" /proc/cpuinfo | awk "{print \$NF}" | tr "\n" " ")"
echo "cpu_midrs=$(for i in 0 1 2 3 4 5 6 7; do cat $C/cpu$i/regs/identification/midr_el1 2>/dev/null; done | tr "\n" " ")"
echo "cpu_online_type=$(stat -c %F $C/online 2>&1)"
echo "cpu_policies=$(ls $C/cpufreq 2>&1 | tr "\n" " ")"
echo "cpu_driver=$(cat $C/cpu0/cpufreq/scaling_driver 2>&1)"; echo "cpu_idle=$(cat $C/cpuidle/current_driver 2>&1)"
echo "cpu_maxfreq=$(for i in 0 7; do cat $C/cpu$i/cpufreq/cpuinfo_max_freq 2>&1; done | tr "\n" " ")"
echo "cpu_freqs0=$(cat $C/cpu0/cpufreq/scaling_available_frequencies 2>&1 | cut -c1-60)"
'
: > "$DIR/adb.txt"; rm -f "$DIR"/*.kv "$DIR/stream.txt"
while read -r id ver g cc px mdl; do
  [ "$(docker inspect -f '{{.State.Running}}' "pp-$id" 2>/dev/null)" = true ] || { echo "$id 没开起来" >> "$DIR/adb.txt"; continue; }
  ip=$(curl -s -H "$H" "$API/phones" | python3 -c 'import sys,json
for p in json.load(sys.stdin):
    if p["id"]==sys.argv[1]: print(p.get("ip",""))' "$id" 2>/dev/null)
  # 和用户一样：先在管理页打开 USB 调试（默认关着，踩坑 #56），再授权电脑的公钥
  curl -s -m60 -X PUT -H "$H" -H 'Content-Type: application/json' -d '{"adb":true}' "$API/phones/$id" -o /dev/null
  curl -s -m20 -X POST -H "$H" -H 'Content-Type: application/json' -d "{\"key\":\"$KEY\"}" "$API/phones/$id/adb-key" -o /dev/null
  sleep 2
  # adb 会读标准输入：不重定向的话会把 while 循环要读的列表吃掉，只测到第一台
  # 刚打开 USB 调试时 adbd 要重启，开机慢的那台（几台一起开、负载高）2 秒内可能还没起来：最多试 20 秒
  # 上一轮的手机可能用过同一个 IP：adb 还留着那条（已离线的）旧连接，connect 只回「already connected」
  adb disconnect "$ip:5555" </dev/null >/dev/null 2>&1
  for _try in 1 2 3 4 5 6 7 8 9 10; do
    c=$(adb connect "$ip:5555" </dev/null 2>&1 | tr '\n' ' ')
    st=$(adb -s "$ip:5555" get-state </dev/null 2>&1)
    [ "$st" = device ] && break
    adb disconnect "$ip:5555" </dev/null >/dev/null 2>&1
    sleep 2
  done
  echo "$id ip=$ip connect=[$c] state=$st" >> "$DIR/adb.txt"
  # 连不上时把网络现场留下来（手机删了就查不到了）：宿主机邻居表 / 路由 / ping，手机里的网卡、地址、路由、adbd
  if [ "$st" != device ]; then
    { echo "== 宿主机"; ip neigh show "$ip"; ip route get "$ip"; ping -c2 -W2 "$ip" 2>&1 | tail -2
      echo "== docker"; docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} ip={{$v.IPAddress}} mac={{$v.MacAddress}}{{end}} pid={{.State.Pid}}' "pp-$id"
      pid=$(docker inspect -f '{{.State.Pid}}' "pp-$id"); [ -n "$pid" ] && nsenter -t "$pid" -n sh -c 'ip -br link; ip -br addr; ip route; ip neigh'
      echo "== 崩溃"; crashdump "$id"
      echo "== 手机里"; pex "pp-$id" sh -c 'getprop init.svc.adbd; getprop service.adb.tcp.port; getprop persist.adb.tcp.port; ip -br addr; ip rule | head -20; ip route show table all | grep -v "^local\|^broadcast" | head -20; ss -ltn 2>/dev/null | head' 2>&1
    } > "$DIR/$id.net-debug.txt" 2>&1
  fi
  {
    echo "id=$id"; echo "spec_ver=$ver"; echo "spec_gapps=$g"; echo "spec_cc=$cc"; echo "spec_proxy=$px"; echo "spec_model=${mdl:-}"
    echo "boot_secs=$(grep "^$id " "$DIR/boot.txt" | cut -d' ' -f2)"
    echo "adb_state=$st"; echo "adb_connect=$c"
    echo "exit_ip=$(python3 -c 'import sys,json
d=json.load(open(sys.argv[1])); d=d.get("proxies",d) if isinstance(d,dict) else d
for p in d:
    if p["id"]==sys.argv[2]: c=p.get("check") or p.get("lastCheck") or {}; print(c.get("exitIp") or c.get("ip") or "")' "$DIR/proxies.json" "$px" 2>/dev/null)"
    adb -s "$ip:5555" shell "$PULL" </dev/null 2>&1
    echo "adb_root=$(adb -s "$ip:5555" root </dev/null 2>&1 | tr '\n' ' ')"
  } > "$DIR/$id.kv"
done < "$DIR/phones.txt"
log "ADB 拉值完成（$(grep -c 'state=device' "$DIR/adb.txt")/$(wc -l < "$DIR/phones.txt") 台连上）"

# ---- 同时看 N 路实时画面 30 秒 ----
log "同时开 $(wc -l < "$DIR/phones.txt") 路实时画面 30 秒"
while read -r id _; do
  [ "$(docker inspect -f '{{.State.Running}}' "pp-$id" 2>/dev/null)" = true ] || continue
  ( b=$(curl -s -m30 -H "$H" "$API/phones/$id/stream?w=720&h=1600" -o "$DIR/$id.h264" -w '%{size_download}')
    # 最大关键帧多大：纯黑 / 纯色画面的关键帧只有一两 KB；画面不动时总数据量也很小，但关键帧照样有十几 KB（看总量会误报）
    idr=$(python3 -c "
import re,sys
d=open(sys.argv[1],'rb').read(); p=[m.start() for m in re.finditer(b'\x00\x00\x00\x01',d)]+[len(d)]
print(max([b-a for a,b in zip(p,p[1:]) if (d[a+4]&31)==5] or [0]))" "$DIR/$id.h264" 2>/dev/null)
    rm -f "$DIR/$id.h264"; echo "$id $b ${idr:-0}" >> "$DIR/stream.txt" ) &
  # 画面要动起来才像真在用（静止画面编码几乎不耗 CPU）：每 2 秒左右滑一下桌面
  pex "pp-$id" sh -c 'for i in $(seq 1 14); do input swipe 900 1300 150 1300 250; sleep 1; input swipe 150 1300 900 1300 250; sleep 1; done' </dev/null >/dev/null 2>&1 &
done < "$DIR/phones.txt"
# 每台实时画面用的编码器：c2.rk.* = 硬件（#133），c2.android.* = 软件。开画面后马上查：
# 谷歌版日志多，logcat 缓冲只有 256KB，等 30 秒画面放完再查，分配编码器那行早被冲掉了（2026-11-23）
sleep 5
while read -r id _; do
  [ "$(docker inspect -f '{{.State.Running}}' "pp-$id" 2>/dev/null)" = true ] || continue   # 没开起来的在汇总里单独报
  enc=$(pex "pp-$id" sh -c 'logcat -d | grep -o "allocate(c2[.a-z0-9]*encoder)" | tail -1' </dev/null 2>/dev/null)
  echo "$id ${enc:-?}"
  # 落到软件编码时把硬件编码器为什么没用上留下来（mpp / c2 报错、SELinux 拒绝）
  case "$enc" in *c2.android*) { pex "pp-$id" sh -c 'logcat -d | grep -iE "mpp|c2.rk|codec2|avc: denied" | tail -80'; crashdump "$id"; } > "$DIR/$id.encoder-fallback.txt" 2>&1 ;; esac
done < "$DIR/phones.txt" > "$DIR/encoder.txt"
sleep 10
{
  echo "== 同时看画面时（第 15 秒）"
  echo "load: $(cut -d' ' -f1-3 /proc/loadavg)  温度: $(( $(cat /sys/class/thermal/thermal_zone0/temp)/1000 ))℃"
  top -bn1 | head -5
} > "$DIR/host-stream.txt"
wait
log "画面测试完成"

# ---- 汇总 ----
python3 - "$DIR" <<'EOF' | tee "$DIR/report.txt"
import os, re, sys, glob
d = sys.argv[1]
rows = []
for f in sorted(glob.glob(os.path.join(d, "*.kv"))):
    kv = {}
    for line in open(f, errors="replace"):
        if "=" in line:
            k, v = line.rstrip("\n").split("=", 1)
            kv.setdefault(k, v)
    s = kv.get("imei_raw", "")
    digits = re.sub(r"[^0-9]", "", "".join(re.findall(r"'([^']*)'", s)))
    kv["imei"] = digits if len(digits) == 15 else ("拒绝:" + s[:60] if "Exception" in s or "denied" in s.lower() else "")
    rows.append(kv)
n = len(rows)
cc_tz = {"JP": "Asia/Tokyo", "HK": "Asia/Hong_Kong", "SG": "Asia/Singapore", "TW": "Asia/Taipei", "KR": "Asia/Seoul", "US": "America/"}
print(f"===== 多开 {n} 台 · ADB 拉值 =====")
fmt = "{:<13} {:>3} {:<4} {:<3} {:<11} {:>4}s {:<7} {:<20} {:<12} {:<6} {:<15} {:<4} {:<3}"
print(fmt.format("id", "ver", "谷歌", "国", "机型", "开机", "adb", "时区", "运营商", "网络", "IMEI", "蓝牙", "痕迹"))
for r in rows:
    print(fmt.format(r.get("id", ""), r.get("ro.build.version.release", "?"), "是" if r.get("spec_gapps") == "true" else "", r.get("spec_cc", ""),
                     r.get("ro.product.model", "")[:11], r.get("boot_secs", "?"), r.get("adb_state", "")[:7], r.get("persist.sys.timezone", "")[:20], r.get("gsm.operator.alpha", "")[:12],
                     r.get("gsm.network.type", "")[:6], r.get("imei", "")[:15], r.get("bt_state", ""), r.get("tellprops", "?")))
print()
def uniq(k):
    v = [r.get(k, "") for r in rows if r.get(k, "") and "rror" not in r.get(k, "") and "denied" not in r.get(k, "")]
    return f"{len(set(v))}/{len(v)}" + ("" if len(v) == n else f"（{n-len(v)} 台读不到）")
print("唯一性：android_id " + uniq("android_id") + "  序列号 " + uniq("ro.serialno") + "  IMEI " + uniq("imei") +
      "  蓝牙MAC " + uniq("bt_addr") + "  WiFi MAC " + uniq("wifi_mac") + "  boot_id " + uniq("boot_id") + "  开机时长 " + uniq("uptime"))
import json
models = {}
try:
    md = json.load(open(os.path.join(d, "models.json"))); md = md.get("models", md) if isinstance(md, dict) else md
    models = {m["id"]: m for m in md}
except Exception: pass
bad = []
for r in rows:
    m = models.get(r.get("spec_model", ""))
    if m:
        mv = m.get("values") or {}
        for k, pk in (("brand", "ro.product.brand"), ("model", "ro.product.model"), ("device", "ro.product.device")):
            if mv.get(k) and r.get(pk) and mv[k].lower() != r[pk].lower(): bad.append(f"{r.get('id')} 机型 {m['id']} 的 {k} 应是 {mv[k]}，手机里是 {r[pk]}")
        tacs = m.get("imeiTacs") or []
        if tacs and r.get("imei", "").isdigit() and r["imei"][:8] not in tacs: bad.append(f"{r.get('id')} IMEI 前 8 位 {r['imei'][:8]} 不是机型 {m['id']} 的真实 TAC（{'/'.join(tacs)}）")
        if r.get("ro.build.version.release") and int(r.get("spec_ver", "0")) != int(re.sub(r"\D.*", "", r["ro.build.version.release"]) or 0): bad.append(f"{r.get('id')} 要安卓 {r.get('spec_ver')}，手机里是 {r.get('ro.build.version.release')}")
    cc, i = r.get("spec_cc", ""), r.get("id", "")
    if r.get("adb_state") != "device":
        bad.append(f"{i} ADB 没连上（{r.get('adb_state','')}；连接：{r.get('adb_connect','')[:80]}），网络现场在 {i}.net-debug.txt")
        continue  # 值都没拉到，后面的检查没意义
    if cc in cc_tz and not r.get("persist.sys.timezone", "").startswith(cc_tz[cc]): bad.append(f"{i} 代理 {cc} 但时区 {r.get('persist.sys.timezone')}")
    if cc not in ("direct", "") and r.get("gsm.sim.operator.iso-country", "").upper() != cc: bad.append(f"{i} 代理 {cc} 但 SIM 国家 {r.get('gsm.sim.operator.iso-country')}")
    if cc not in ("direct", "") and not r.get("gsm.operator.alpha"): bad.append(f"{i} 没运营商")
    if not r.get("imei", "").isdigit(): bad.append(f"{i} ADB 读不到 IMEI（{r.get('imei','')[:50]}）")
    if r.get("bt_state") != "ON": bad.append(f"{i} 蓝牙 {r.get('bt_state')}")
    if not r.get("gles", "").count("Mali"): bad.append(f"{i} 没有 Mali 硬件加速")
    if r.get("tellprops", "0") not in ("0", ""): bad.append(f"{i} getprop 里有 {r.get('tellprops')} 条模拟器/容器字样：{r.get('tellprops_list','')[:120]}")
    if r.get("mount_tells", "0") not in ("0", ""): bad.append(f"{i} /proc/mounts 有 {r.get('mount_tells')} 条容器字样")
    if r.get("dev_tells", "0") not in ("0", ""): bad.append(f"{i} /dev 有 {r.get('dev_tells')} 个模拟器设备名")
    # 指纹 / 版本号里的工程编译字样（机型档案没这个安卓版本的真指纹时，兜底拼出来的会带 eng.*，2026-11-23）
    fpv = " ".join(r.get(k, "") for k in ("ro.build.fingerprint", "ro.vendor.build.fingerprint", "ro.build.version.incremental"))
    if re.search(r"eng\.|test-keys|userdebug|redroid|frank", fpv): bad.append(f"{i} 指纹里有工程版字样：{r.get('ro.build.fingerprint','')[:110]}")
    # CPU（踩坑 #147）：cpuinfo 的核心型号和每个核的 midr 文件要一致；/sys/devices/system/cpu 下要是普通文件（魔云腾内核原样会 stat 成目录）；
    # 有真机 CPU 目录模板的机型不能再露 RK3588 的调频驱动 / 频率表
    parts = r.get("cpu_parts", "").split(); midrs = r.get("cpu_midrs", "").split()
    if parts and midrs and [p.lower() for p in parts] != ["0x%03x" % ((int(m, 16) >> 4) & 0xfff) for m in midrs]:
        bad.append(f"{i} cpuinfo 的核心型号 {' '.join(parts)} 和 midr 文件 {' '.join(midrs)} 对不上")
    # Vulkan（踩坑 #148）：要能枚举到 Mali 显卡；驱动目录里只能有 Mali 的入口模块（redroid 的开源驱动是痕迹）
    if not r.get("vk_device", "").startswith("Mali"): bad.append(f"{i} Vulkan 枚举不到显卡（{r.get('vk_device') or '0 个设备'}）")
    # 只能有一个入口模块：一致档 vulkan.mali.so；伪装档是真机的名字（vulkan.adreno.so），镜像自带的 mali 那个开机时删掉
    if len(r.get("vk_drivers", "").split()) > 1: bad.append(f"{i} /vendor/lib64/hw 里有不止一个 Vulkan 驱动：{r.get('vk_drivers')}")
    mm = models.get(r.get("spec_model", "")) or {}
    # 机型第二层（踩坑 #151）：机型录了什么，手机里就要照真机
    mv2 = mm.get("values") or {}
    if mv2.get("kernelRelease") and r.get("uname") and r["uname"] != mv2["kernelRelease"]: bad.append(f"{i} 内核版本 {r['uname']}，机型的是 {mv2['kernelRelease']}")
    if mm.get("sensors") and ("MTK" in r.get("sensor_vendors", "").split(";") or not r.get("sensor_vendors")): bad.append(f"{i} 传感器还是通用名 / 厂商 MTK（没换成真机的）：{r.get('sensor_vendors','')[:100]}")
    if mm.get("features") and r.get("feat_ethernet") not in ("0", ""): bad.append(f"{i} 系统特性没换成真机的（还有 android.hardware.ethernet，共 {r.get('features_n')} 项）")
    if mm.get("codecs") and r.get("codec_rk_alias") in ("0", ""): bad.append(f"{i} 编解码器列表里没有真机的名字")
    if mm.get("dataBytes") and r.get("data_kb", "").isdigit() and abs(int(r["data_kb"]) * 1024 - mm["dataBytes"]) > mm["dataBytes"] * 0.01: bad.append(f"{i} /data 容量 {int(r['data_kb'])//1024//1024}GB，真机 {mm['dataBytes']//1024**3}GB")
    if mm.get("cpuTree"):
        denied = [k for k in ("cpu_online_type", "cpu_policies", "cpu_driver", "cpu_maxfreq") if "Permission denied" in r.get(k, "")]
        if denied: bad.append(f"{i} CPU 目录 App 读不了（{'、'.join(denied)} 权限被拒：换上去的目录 SELinux 标签不对，踩坑 #147）")
        # online 在魔云腾内核上 stat 出来是目录：内核按路径处理 /sys/devices/system/cpu，换上去的普通文件也一样
        # （魔云腾自家云手机同样如此），改不了，不算问题，见报告末尾的已知限制
        if r.get("cpu_driver") == "cpufreq-dt" or r.get("cpu_freqs0", "").startswith("408000 600000 816000"): bad.append(f"{i} CPU 目录还是 RK3588 的（{r.get('cpu_driver')}，{r.get('cpu_freqs0')}）")
    if r.get("ro.debuggable") != "0" or r.get("ro.build.type") != "user": bad.append(f"{i} 构建类型 {r.get('ro.build.type')} debuggable={r.get('ro.debuggable')}")
    if "cannot run as root" not in r.get("adb_root", "") : bad.append(f"{i} adb root 结果：{r.get('adb_root','')[:80]}")
    if r.get("spec_gapps") == "true" and not (r.get("gms", "0").isdigit() and int(r.get("gms", "0")) >= 1): bad.append(f"{i} 谷歌版没有 GMS")
# 没开起来的（开机请求被拒、开机超时）：原因从 run.log 里找
booted = set(l.split()[0] for l in open(os.path.join(d, "boot.txt")) if l.strip()) if os.path.exists(os.path.join(d, "boot.txt")) else None
runlog = open(os.path.join(d, "run.log"), errors="replace").read() if os.path.exists(os.path.join(d, "run.log")) else ""
if booted is not None and os.path.exists(os.path.join(d, "phones.txt")):
    for line in open(os.path.join(d, "phones.txt")):
        a = line.split()
        if not a or a[0] in booted: continue
        why = re.search(re.escape(a[0]) + r" 开机请求失败：(.*)", runlog)
        bad.append(f"{a[0]}（安卓 {a[1]}{'谷歌' if a[2] == 'true' else ''} {a[5] if len(a) > 5 else ''}）没开起来：" + (why.group(1)[:160] if why else "开机超时"))
enc = {}
try:
    for line in open(os.path.join(d, "encoder.txt")):
        a = line.split()
        if len(a) >= 2: enc[a[0]] = a[1]
except OSError: pass
for i, e in sorted(enc.items()):
    if "c2.rk." not in e: bad.append(f"{i} 实时画面没用上硬件编码（{e}）" + ("，原因看 %s.encoder-fallback.txt" % i if "c2.android" in e else ""))
# 画面有没有内容（2026-11-24 伪装档扩展列表让界面画不出来，只查编码器时漏掉了）：看最大关键帧，纯黑的只有一两 KB
try:
    for line in open(os.path.join(d, "stream.txt")):
        a = line.split()
        if len(a) >= 3 and a[2].isdigit() and int(a[2]) < 5000:
            bad.append(f"{a[0]} 实时画面最大的关键帧只有 {int(a[2])} 字节（30 秒共 {int(a[1]) // 1024} KB）：黑屏 / 纯色")
except OSError: pass
# 有问题的手机 ID（KEEP_BAD=1 时收拾阶段不删，留着查）
with open(os.path.join(d, "bad-ids.txt"), "w") as fh:
    fh.write("\n".join(sorted(set(b.split()[0] for b in bad if re.match(r"^[0-9a-f]{12}", b)))) + "\n")
print()
print("问题：" if bad else "问题：无")
for b in bad: print("  - " + b)
print("已知限制（改不了，不算问题）：/sys/devices/system/cpu 下的文件 stat 出来是目录（魔云腾内核按路径处理，魔云腾自家云手机也一样）")
print()
r0 = rows[0] if rows else {}
print("样例（第一台）：机型 %s / %s / %s，安卓 %s，指纹 %s" % (r0.get("ro.product.brand"), r0.get("ro.product.model"), r0.get("ro.product.device"), r0.get("ro.build.version.release"), r0.get("ro.build.fingerprint")))
print("  屏幕 %s %s，CPU %s，核 %s，%s，内核 %s，%s" % (r0.get("wm_size"), r0.get("wm_density"), r0.get("cpu_hw"), r0.get("nproc"), r0.get("memtotal"), r0.get("uname"), r0.get("gles", "")[:60]))
print("  电池 %s，WiFi %s %s，网卡 %s，传感器 %s，ping %s" % (r0.get("battery"), r0.get("wifi_ssid"), r0.get("wifi_mac"), r0.get("ifaces"), r0.get("sensors"), r0.get("ping")))
for r in rows:
    print("  CPU %s：核 %s | midr %s | 调频 %s / 空闲 %s | 分组 %s | 最高 %s | 文件类型 %s" % (r.get("id"), r.get("cpu_parts", "").strip(), r.get("cpu_midrs", "").strip()[:60], r.get("cpu_driver"), r.get("cpu_idle"), r.get("cpu_policies", "").strip(), r.get("cpu_maxfreq", "").strip(), r.get("cpu_online_type")))
print()
for f in ("host-idle.txt", "host-stream.txt", "stream.txt", "encoder.txt", "adb.txt"):
    p = os.path.join(d, f)
    if os.path.exists(p):
        print(f"--- {f}"); print(open(p).read().rstrip())
EOF

# ---- 收拾 ----
if [ "${KEEP:-0}" = 1 ]; then log "KEEP=1：手机留着"; exit 0; fi
while read -r id _; do
  if [ "${KEEP_BAD:-0}" = 1 ] && grep -qx "$id" "$DIR/bad-ids.txt" 2>/dev/null; then log "KEEP_BAD=1：$id 有问题，留着"; continue; fi
  ip=$(grep "^$id " "$DIR/adb.txt" | sed -n 's/.* ip=\([0-9.]*\) .*/\1/p'); [ -n "$ip" ] && adb disconnect "$ip:5555" >/dev/null 2>&1
  docker kill "pp-$id" >/dev/null 2>&1; curl -s -m60 -X DELETE -H "$H" "$API/phones/$id" -o /dev/null
done < "$DIR/phones.txt"
ids=$(cut -d' ' -f1 "$DIR/phones.txt" | { [ "${KEEP_BAD:-0}" = 1 ] && grep -vxFf "$DIR/bad-ids.txt" || cat; } | tr '\n' ' ')
curl -s -m20 -H "$H" "$API/trash" | python3 -c 'import sys,json
ids=set(sys.argv[1:])
for t in json.load(sys.stdin) or []:
    if t.get("phoneId") in ids: print(t.get("name",""))' $ids 2>/dev/null | while read -r t; do curl -s -m30 -X DELETE -H "$H" "$API/trash/$t" -o /dev/null; done
log "删净。结果在 $DIR/report.txt"
