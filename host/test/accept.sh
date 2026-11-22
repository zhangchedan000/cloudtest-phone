#!/bin/sh
# 验收 / 压测：建 N 台手机 → 开机 → 全项检查 → 删掉（连回收站一起清）。在板子上 root 跑。
#
# 用法：sh host/test/accept.sh <安卓版本> <台数> [每批几台=6] [谷歌版 0/1=0] [代理ID=direct-dev]
#   sh host/test/accept.sh 16 6                    # 安卓16 普通版 6 台，直连
#   sh host/test/accept.sh 15 4 4 1                # 安卓15 谷歌版 4 台
#   sh host/test/accept.sh 14 1 1 0 p-xxxx         # 安卓14 挂真代理（代理库里的 ID）1 台
#   sh host/test/accept-all.sh                     # 14/15/16 普通 + 谷歌全套
#
# 结果：/mmc/pp-build/accept-<标签>.tsv（每台一行）+ 最后一行汇总（唯一性、各项通过数）。
# 口令从 /etc/conf.d/prism-phone-agent 读（也可以 PP_TOKEN=… 传）。KEEP_FAILED=1：蓝牙没开 / 系统崩过 / 真代理没注册上运营商的手机留着不删，方便进去查。
# 蓝牙没开的手机会把诊断（bluetooth_manager + 相关日志）存到 /mmc/pp-build/accept-<标签>-bt-<id>.log。拆机先 docker kill 再删（踩坑 #116）。
# 检查项说明见文件末尾。
set -u
VER=${1:?安卓版本}; TOTAL=${2:?台数}; BATCH=${3:-6}; GAPPS=${4:-0}; PROXY=${5:-direct-dev}
API=http://127.0.0.1:8099/api
TOK=${PP_TOKEN:-$(sed -n 's/^PP_TOKEN="\(.*\)"$/\1/p' /etc/conf.d/prism-phone-agent 2>/dev/null)}
[ -n "$TOK" ] || { echo "拿不到口令（/etc/conf.d/prism-phone-agent 或 PP_TOKEN）"; exit 1; }
H="Authorization: Bearer $TOK"
DIR=/mmc/pp-build; mkdir -p "$DIR"
tag="v$VER"; [ "$GAPPS" = 1 ] && tag="${tag}g"; [ "$PROXY" != direct-dev ] && tag="${tag}-rp"
OUT=$DIR/accept-$tag.tsv
printf 'id\tboot\tmali\tsensors\tphone\tdialer\tgms\tfatal\tphonecrash\timei\tsim\toperator\tnet\tbt\tbtcrash\tandroidId\tserial\tbootid\tfp\tradiocrash\ttells\tvalidated\twifi\n' > "$OUT"
log(){ echo "$(date +%H:%M:%S) $*"; }
gfield=""; [ "$GAPPS" = 1 ] && gfield=',"gapps":true'

# IMEI：安卓14/15/16 的 iphonesubinfo 第 1 号调用参数一样（callingPackage）；读出 15 位数字才算
imei_of(){
  docker exec "pp-$1" sh -c "service call iphonesubinfo 1 s16 com.android.shell 2>/dev/null" |
    python3 -c 'import sys,re;s=sys.stdin.read();c=re.findall(chr(39)+"([^"+chr(39)+"]*)"+chr(39),s);d=re.sub(r"[^0-9]","","".join(c));print(d if len(d)==15 else "")' 2>/dev/null
}
# 回收站里这台的数据也删掉（压测一轮几十台，不清的话回收站很快几十 G）
purge_trash(){
  curl -s -m20 -H "$H" "$API/trash" | python3 -c 'import sys,json
ids=set(sys.argv[1:])
for t in json.load(sys.stdin) or []:
    if t.get("phoneId") in ids: print(t.get("name",""))' "$@" 2>/dev/null | while read -r n; do
    curl -s -m30 -X DELETE -H "$H" "$API/trash/$n" -o /dev/null
  done
}

done_n=0; b=0
while [ "$done_n" -lt "$TOTAL" ]; do
  b=$((b+1)); n=$((TOTAL-done_n)); [ "$n" -gt "$BATCH" ] && n=$BATCH
  temp=$(( $(cat /sys/class/thermal/thermal_zone0/temp)/1000 ))
  [ "$temp" -gt 78 ] && { log "温度 ${temp}℃ > 78，等 60 秒降温"; sleep 60; }
  log "第 $b 批：建 $n 台（已完成 $done_n/$TOTAL，温度 ${temp}℃，根分区剩 $(df -h / | awk 'NR==2{print $4}')）"
  ids=""; keep=""
  for i in $(seq 1 "$n"); do
    body="{\"name\":\"accept-$tag-$b-$i\",\"modelId\":\"poco-x4-gt\",\"androidVersion\":$VER,\"proxyId\":\"$PROXY\"$gfield}"
    id=$(curl -s -m20 -X POST -H "$H" -H 'Content-Type: application/json' -d "$body" "$API/phones" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id",""))' 2>/dev/null)
    if [ -n "$id" ]; then
      ids="$ids $id"
      sr=$(curl -s -m120 -w '\n%{http_code}' -X POST -H "$H" "$API/phones/$id/start")
      case "$(echo "$sr" | tail -1)" in 2*) ;; *) log "  $id 开机请求失败：$(echo "$sr" | head -c 300 | tr '\n' ' ')" ;; esac
    else log "  第 $i 台建失败"; fi
  done
  for id in $ids; do
    for _ in $(seq 1 40); do
      [ "$(docker inspect -f '{{.State.Running}}' "pp-$id" 2>/dev/null)" != true ] && break
      [ "$(docker exec "pp-$id" getprop sys.boot_completed 2>/dev/null)" = 1 ] && break
      sleep 5
    done
  done
  sleep 40   # 开机后设置、电话注册、蓝牙起来要一会儿
  for id in $ids; do
    C=pp-$id
    r=$(docker exec "$C" sh -c '
      bc=$(getprop sys.boot_completed)
      mali=$(dumpsys SurfaceFlinger 2>/dev/null | grep -oE "Mali-G[0-9]+" | head -1)
      sens=$(dumpsys sensorservice 2>/dev/null | grep -icE "android.sensor")
      ph=$(service check iphonesubinfo 2>/dev/null | grep -oE "not found|found")
      dl=$(pm list packages 2>/dev/null | grep -cE "com.android.dialer|com.android.messaging")
      fa=$(logcat -d -b crash 2>/dev/null | grep -cE "FATAL EXCEPTION IN SYSTEM PROCESS")
      pc=$(logcat -d -b crash 2>/dev/null | grep -c "Process: com.android.phone")
      sim=$(getprop gsm.sim.state); op=$(getprop gsm.operator.alpha); net=$(getprop gsm.network.type)
      bt=$(dumpsys bluetooth_manager 2>/dev/null | grep -m1 -E "^ *state:" | tr -d " " | cut -d: -f2)
      btc=$(logcat -d -b crash 2>/dev/null | grep -c "Process: com.android.bluetooth")
      rcc=$(logcat -d -b crash 2>/dev/null | grep -cE "Cmdline: /vendor/bin/hw/android.hardware.radio-service")
      tl=$(getprop | grep -ciE "qemu|goldfish|ranchu|redroid|cuttlefish")
      vd=$(dumpsys connectivity 2>/dev/null | grep -m1 "NetworkAgentInfo" | grep -c "IS_VALIDATED")
      wf=$(dumpsys connectivity 2>/dev/null | grep -m1 "NetworkAgentInfo" | grep -c "ni{WIFI CONNECTED")
      aid=$(settings get secure android_id 2>/dev/null)
      printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s" \
        "$bc" "${mali:-NONE}" "$sens" "${ph:-ERR}" "$dl" GMS "$fa" "$pc" SIM "${sim:-?}" "${op:-}" "${net:-}" "${bt:-?}" "$aid" "$(getprop ro.serialno)" "$(getprop ro.build.fingerprint)" "$btc" "$rcc" "$tl" "$vd" "$wf"' 2>/dev/null)
    gms=NA
    [ "$GAPPS" = 1 ] && gms=$(docker exec "$C" sh -c 'p=$(pidof com.google.android.gms); v=$(pm list packages com.android.vending | wc -l); echo "gms${p:+Y}${p:-N}/play$v"' 2>/dev/null)
    imei=$(imei_of "$id"); bootid=$(docker exec "$C" cat /proc/sys/kernel/random/boot_id 2>/dev/null)
    # 按列拼：把占位的 GMS / SIM 换成实际值
    echo "$r" | awk -F'\t' -v OFS='\t' -v id="$id" -v gms="$gms" -v imei="$imei" -v bootid="$bootid" \
      '{print id,$1,$2,$3,$4,$5,gms,$7,$8,imei,$10,$11,$12,$13,$17,$14,$15,bootid,$16,$18,$19,$20,$21}' >> "$OUT"
    bt=$(echo "$r" | cut -f13); fa=$(echo "$r" | cut -f7)
    [ "$VER" = 13 ] && bt=ON   # 安卓 13 没有蓝牙模拟，关着是正常的
    if [ "$bt" != ON ]; then
      { echo "== $id 蓝牙=$bt $(date '+%F %T')"; docker exec "$C" sh -c 'dumpsys bluetooth_manager 2>/dev/null | head -60; echo ==; logcat -d 2>/dev/null | grep -iE "BluetoothManagerService|bluetooth.*(hal|hci)|AdapterState|btif|Bluetooth.*(crash|died|timeout)" | tail -150; echo ==crash; logcat -d -b crash 2>/dev/null | tail -60'
        echo "== 宿主："; pgrep -af "rootcanal.*$id" ; ls -la /run/ppbt-*"$id"* 2>/dev/null; } > "$DIR/accept-$tag-bt-$id.log" 2>&1
      log "  $id 蓝牙=$bt，诊断存到 $DIR/accept-$tag-bt-$id.log"
    fi
    op=$(echo "$r" | cut -f11)
    if [ "${KEEP_FAILED:-0}" = 1 ] && { [ "$bt" != ON ] || [ "$fa" != 0 ] || { [ "$PROXY" != direct-dev ] && [ -z "$op" ]; }; }; then keep="$keep $id"; fi
  done
  for id in $ids; do
    case " $keep " in *" $id "*) log "  留着 $id（KEEP_FAILED=1）"; continue ;; esac
    docker kill "pp-$id" >/dev/null 2>&1; curl -s -m60 -X DELETE -H "$H" "$API/phones/$id" -o /dev/null; done
  # shellcheck disable=SC2086
  purge_trash $ids
  done_n=$((done_n+n))
  log "第 $b 批完成并删净（已完成 $done_n/$TOTAL）"
done

# 汇总
python3 - "$OUT" "$GAPPS" "$PROXY" "$VER" <<'EOF'
import csv, sys
rows = list(csv.DictReader(open(sys.argv[1]), delimiter='\t'))
gapps, proxy, ver = sys.argv[2] == "1", sys.argv[3], int(sys.argv[4])
# 安卓 13（cnflysky lineage 底子）：没做蓝牙模拟（镜像 prism.bt=0，蓝牙本来就关着），模拟器传感器 14 个
min_sensors = 14 if ver == 13 else 16
n = len(rows)
def cnt(f): return sum(1 for r in rows if f(r))
def uniq(k):
    vals = [r[k] for r in rows if r[k]]
    return f"{len(set(vals))}/{n}"
items = [
    ("开机", cnt(lambda r: r["boot"] == "1")),
    ("Mali", cnt(lambda r: r["mali"].startswith("Mali"))),
    (f"传感器≥{min_sensors}", cnt(lambda r: r["sensors"].isdigit() and int(r["sensors"]) >= min_sensors)),
    ("电话服务", cnt(lambda r: r["phone"] == "found")),
    ("拨号短信", cnt(lambda r: r["dialer"].isdigit() and int(r["dialer"]) >= 2)),
    ("无系统崩溃", cnt(lambda r: r["fatal"] == "0")),
    ("电话进程不崩", cnt(lambda r: r["phonecrash"] == "0")),
    ("读到IMEI", cnt(lambda r: len(r["imei"]) == 15)),
    ("蓝牙ON", cnt(lambda r: r["bt"] == "ON" or ver == 13)),
    ("蓝牙不崩", cnt(lambda r: r["btcrash"] == "0")),
    ("电话HAL不崩", cnt(lambda r: r["radiocrash"] == "0")),
    ("属性无模拟器字样", cnt(lambda r: r.get("tells") == "0")),
    ("联网验证通过", cnt(lambda r: r.get("validated") == "1")),
    ("走WiFi", cnt(lambda r: r.get("wifi") == "1")),
]
if gapps: items.append(("GMS+Play", cnt(lambda r: r["gms"].startswith("gmsY") and r["gms"].endswith("play1"))))
if proxy != "direct-dev":
    items.append(("SIM就绪", cnt(lambda r: r["sim"] in ("READY", "LOADED"))))
    items.append(("有运营商", cnt(lambda r: r["operator"] != "")))
print(f"台数={n} | " + "  ".join(f"{k}:{v}" for k, v in items))
print(f"唯一：IMEI {uniq('imei')}  androidId {uniq('androidId')}  序列号 {uniq('serial')}  bootid {uniq('bootid')}")
bad = [k for k, v in items if v != n]
print("✅ 全部通过" if n and not bad else f"⚠ 没全过：{'、'.join(bad) if bad else '没有手机'}")
EOF
echo "结果：$OUT"

# 检查项：
#   boot 开机完成；mali 显卡是 Mali-G610（硬件加速）；sensors 传感器个数（≥16）；
#   phone 电话服务 iphonesubinfo 注册（found）；dialer 拨号 + 短信装上（2）；gms 谷歌服务进程 + Play 商店；
#   fatal system_server 崩溃次数（0）；phonecrash com.android.phone 崩溃次数（0）；
#   imei App 层读到的 15 位 IMEI（每台唯一）；sim / operator / net 真代理时 SIM 状态、运营商、网络制式；
#   bt 蓝牙状态（宿主装了 rootcanal 时应为 ON，踩坑 #122；安卓 13 没有蓝牙模拟、不算）；sensors 安卓 13 是 14 个；btcrash com.android.bluetooth 崩溃次数；radiocrash 电话 HAL（radio-service）崩溃次数（0，踩坑 #125）；tells getprop 里 qemu/goldfish/ranchu/redroid 字样条数（0，踩坑 #127）；validated 安卓自己的联网检测（DNS + 访问 generate_204）通过（1，踩坑 #131）；wifi 安卓当前网络是 WiFi（1，宿主机有 mac80211_hwsim 且管家没带 -no-wifi 时，踩坑 #129）；androidId / serial / bootid 身份（每台唯一）。
