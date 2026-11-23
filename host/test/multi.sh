#!/bin/sh
# 多开 + ADB 拉值：同时开 N 台（安卓 14/15/16 普通 / 谷歌混着、挂不同国家的代理），开满后：
#   1) 用 ADB（和用户电脑连手机同一条路：adb connect <手机IP>:5555，shell 身份）把各项值读出来；
#   2) 查唯一性、跟代理国家是否对得上（时区 / 语言 / SIM / 运营商）、容器痕迹；
#   3) 同时开 N 路实时画面 30 秒，看 CPU / 温度 / 每路码流；
#   4) 删净（KEEP=1 留着）。在板子上 root 跑。
#
# 用法：sh host/test/multi.sh [规格 ...]
#   规格 = <版本>[g]:<国家代码 或 direct>，g = 谷歌版。不给就用默认 8 台：
#   14:JP 14g:HK 15:SG 15g:US 16:TW 16g:KR 16:JP 15:direct
# 结果：/mmc/pp-build/multi-<时间>/（每台一个 .kv 原始值 + report.txt 汇总）
set -u
# 手机开了 SELinux：命令交给手机里的小助手跑（宿主机 docker exec 进去的命令，安卓的服务管家不认，查电话 / 蓝牙 / 软件包
# 全会失败，踩坑 #135）；没开的照旧 docker exec
PP_AGENT=${PP_AGENT:-/opt/prism-phone/prism-phone-agent}
PP_DATA=${PP_DATA:-/mmc/pp-data}
pex() { _c=$1; shift; _s=$PP_DATA/phones/${_c#pp-}/ppc/s
  if [ -S "$_s" ]; then "$PP_AGENT" pp-cmdc "$_s" -- "$@" </dev/null; else docker exec "$_c" "$@" </dev/null; fi; }
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

if [ -z "${REUSE:-}" ]; then
# ---- 建 ----
: > "$DIR/phones.txt"
n=0
for s in $SPECS; do
  n=$((n+1)); ver=${s%%:*}; cc=${s#*:}; g=false
  case $ver in *g) g=true; ver=${ver%g} ;; esac
  px=$(proxy_of "$cc"); [ -n "$px" ] || { log "没有 $cc 的可用代理，跳过 $s"; continue; }
  body="{\"name\":\"multi-$n-$ver$( [ $g = true ] && echo g)-$cc\",\"modelId\":\"poco-x4-gt\",\"androidVersion\":$ver,\"proxyId\":\"$px\",\"gapps\":$g}"
  id=$(curl -s -m20 -X POST -H "$H" -H 'Content-Type: application/json' -d "$body" "$API/phones" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id",""))' 2>/dev/null)
  [ -n "$id" ] && echo "$id $ver $g $cc $px" >> "$DIR/phones.txt" || log "建失败：$s"
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
  ro.build.version.release ro.build.version.sdk ro.build.fingerprint ro.build.type ro.build.tags ro.serialno \
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
'
: > "$DIR/adb.txt"; rm -f "$DIR"/*.kv "$DIR/stream.txt"
while read -r id ver g cc px; do
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
  for _try in 1 2 3 4 5 6 7 8 9 10; do
    c=$(adb connect "$ip:5555" </dev/null 2>&1 | tr '\n' ' ')
    st=$(adb -s "$ip:5555" get-state </dev/null 2>&1)
    [ "$st" = device ] && break
    sleep 2
  done
  echo "$id ip=$ip connect=[$c] state=$st" >> "$DIR/adb.txt"
  {
    echo "id=$id"; echo "spec_ver=$ver"; echo "spec_gapps=$g"; echo "spec_cc=$cc"; echo "spec_proxy=$px"
    echo "boot_secs=$(grep "^$id " "$DIR/boot.txt" | cut -d' ' -f2)"
    echo "adb_state=$st"
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
  ( b=$(curl -s -m30 -H "$H" "$API/phones/$id/stream?w=720&h=1600" -o /dev/null -w '%{size_download}'); echo "$id $b" >> "$DIR/stream.txt" ) &
  # 画面要动起来才像真在用（静止画面编码几乎不耗 CPU）：每 2 秒左右滑一下桌面
  pex "pp-$id" sh -c 'for i in $(seq 1 14); do input swipe 900 1300 150 1300 250; sleep 1; input swipe 150 1300 900 1300 250; sleep 1; done' </dev/null >/dev/null 2>&1 &
done < "$DIR/phones.txt"
sleep 15
{
  echo "== 同时看画面时（第 15 秒）"
  echo "load: $(cut -d' ' -f1-3 /proc/loadavg)  温度: $(( $(cat /sys/class/thermal/thermal_zone0/temp)/1000 ))℃"
  top -bn1 | head -5
} > "$DIR/host-stream.txt"
wait
# 每台实时画面用的编码器：c2.rk.* = 硬件（#133），c2.android.* = 软件
while read -r id _; do
  enc=$(pex "pp-$id" sh -c 'logcat -d | grep -o "allocate(c2[.a-z0-9]*encoder)" | tail -1' </dev/null 2>/dev/null)
  echo "$id ${enc:-?}"
done < "$DIR/phones.txt" > "$DIR/encoder.txt"
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
fmt = "{:<13} {:>3} {:<4} {:<3} {:>4}s {:<7} {:<20} {:<12} {:<6} {:<15} {:<4} {:<3}"
print(fmt.format("id", "ver", "谷歌", "国", "开机", "adb", "时区", "运营商", "网络", "IMEI", "蓝牙", "痕迹"))
for r in rows:
    print(fmt.format(r.get("id", ""), r.get("ro.build.version.release", "?"), "是" if r.get("spec_gapps") == "true" else "", r.get("spec_cc", ""),
                     r.get("boot_secs", "?"), r.get("adb_state", "")[:7], r.get("persist.sys.timezone", "")[:20], r.get("gsm.operator.alpha", "")[:12],
                     r.get("gsm.network.type", "")[:6], r.get("imei", "")[:15], r.get("bt_state", ""), r.get("tellprops", "?")))
print()
def uniq(k):
    v = [r.get(k, "") for r in rows if r.get(k, "") and "rror" not in r.get(k, "") and "denied" not in r.get(k, "")]
    return f"{len(set(v))}/{len(v)}" + ("" if len(v) == n else f"（{n-len(v)} 台读不到）")
print("唯一性：android_id " + uniq("android_id") + "  序列号 " + uniq("ro.serialno") + "  IMEI " + uniq("imei") +
      "  蓝牙MAC " + uniq("bt_addr") + "  WiFi MAC " + uniq("wifi_mac") + "  boot_id " + uniq("boot_id") + "  开机时长 " + uniq("uptime"))
bad = []
for r in rows:
    cc, i = r.get("spec_cc", ""), r.get("id", "")
    if r.get("adb_state") != "device": bad.append(f"{i} ADB 没连上（{r.get('adb_state','')}）")
    if cc in cc_tz and not r.get("persist.sys.timezone", "").startswith(cc_tz[cc]): bad.append(f"{i} 代理 {cc} 但时区 {r.get('persist.sys.timezone')}")
    if cc not in ("direct", "") and r.get("gsm.sim.operator.iso-country", "").upper() != cc: bad.append(f"{i} 代理 {cc} 但 SIM 国家 {r.get('gsm.sim.operator.iso-country')}")
    if cc not in ("direct", "") and not r.get("gsm.operator.alpha"): bad.append(f"{i} 没运营商")
    if not r.get("imei", "").isdigit(): bad.append(f"{i} ADB 读不到 IMEI（{r.get('imei','')[:50]}）")
    if r.get("bt_state") != "ON": bad.append(f"{i} 蓝牙 {r.get('bt_state')}")
    if not r.get("gles", "").count("Mali"): bad.append(f"{i} 没有 Mali 硬件加速")
    if r.get("tellprops", "0") not in ("0", ""): bad.append(f"{i} getprop 里有 {r.get('tellprops')} 条模拟器/容器字样：{r.get('tellprops_list','')[:120]}")
    if r.get("mount_tells", "0") not in ("0", ""): bad.append(f"{i} /proc/mounts 有 {r.get('mount_tells')} 条容器字样")
    if r.get("dev_tells", "0") not in ("0", ""): bad.append(f"{i} /dev 有 {r.get('dev_tells')} 个模拟器设备名")
    if r.get("ro.debuggable") != "0" or r.get("ro.build.type") != "user": bad.append(f"{i} 构建类型 {r.get('ro.build.type')} debuggable={r.get('ro.debuggable')}")
    if "cannot run as root" not in r.get("adb_root", "") : bad.append(f"{i} adb root 结果：{r.get('adb_root','')[:80]}")
    if r.get("spec_gapps") == "true" and not (r.get("gms", "0").isdigit() and int(r.get("gms")) >= 1): bad.append(f"{i} 谷歌版没有 GMS")
print()
print("问题：" if bad else "问题：无")
for b in bad: print("  - " + b)
print()
r0 = rows[0] if rows else {}
print("样例（第一台）：机型 %s / %s / %s，安卓 %s，指纹 %s" % (r0.get("ro.product.brand"), r0.get("ro.product.model"), r0.get("ro.product.device"), r0.get("ro.build.version.release"), r0.get("ro.build.fingerprint")))
print("  屏幕 %s %s，CPU %s，核 %s，%s，内核 %s，%s" % (r0.get("wm_size"), r0.get("wm_density"), r0.get("cpu_hw"), r0.get("nproc"), r0.get("memtotal"), r0.get("uname"), r0.get("gles", "")[:60]))
print("  电池 %s，WiFi %s %s，网卡 %s，传感器 %s，ping %s" % (r0.get("battery"), r0.get("wifi_ssid"), r0.get("wifi_mac"), r0.get("ifaces"), r0.get("sensors"), r0.get("ping")))
print()
for f in ("host-idle.txt", "host-stream.txt", "stream.txt", "encoder.txt", "adb.txt"):
    p = os.path.join(d, f)
    if os.path.exists(p):
        print(f"--- {f}"); print(open(p).read().rstrip())
EOF

# ---- 收拾 ----
if [ "${KEEP:-0}" = 1 ]; then log "KEEP=1：手机留着"; exit 0; fi
while read -r id _; do
  ip=$(grep "^$id " "$DIR/adb.txt" | sed -n 's/.* ip=\([0-9.]*\) .*/\1/p'); [ -n "$ip" ] && adb disconnect "$ip:5555" >/dev/null 2>&1
  docker kill "pp-$id" >/dev/null 2>&1; curl -s -m60 -X DELETE -H "$H" "$API/phones/$id" -o /dev/null
done < "$DIR/phones.txt"
ids=$(cut -d' ' -f1 "$DIR/phones.txt" | tr '\n' ' ')
curl -s -m20 -H "$H" "$API/trash" | python3 -c 'import sys,json
ids=set(sys.argv[1:])
for t in json.load(sys.stdin) or []:
    if t.get("phoneId") in ids: print(t.get("name",""))' $ids 2>/dev/null | while read -r t; do curl -s -m30 -X DELETE -H "$H" "$API/trash/$t" -o /dev/null; done
log "删净。结果在 $DIR/report.txt"
