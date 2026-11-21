#!/bin/bash
# 多开压测 + 身份唯一性自检。用法: bash idtest.sh <VER> <TOTAL> <BATCH> [GAPPS0/1]
# 滚动分批:每批 create->start->等boot->拉身份->docker kill+管家删->下批(删完才开下批)。盯温度。
# 在板子上跑(/mmc/pp-build/.pptok 存管家口令)。结果写 idtest-<tag>.tsv。
# 拆机用 docker kill 先行(管家优雅 stop 走 docker stop 超时要 ~27s,见踩坑 #116)。
set -u
VER=$1; TOTAL=$2; BATCH=$3; GAPPS=${4:-0}
TOK=$(cat /mmc/pp-build/.pptok); H="Authorization: Bearer $TOK"; API=http://127.0.0.1:8099/api
tag="v${VER}"; [ "$GAPPS" = 1 ] && tag="v${VER}g"
OUT=/mmc/pp-build/idtest-$tag.tsv
LOG=/mmc/pp-build/idtest-$tag.log; : > "$LOG"
printf 'id\tboot\tmali\tsensors\tril\tfatal\tandroidId\tserial\tbootid\timei\tbtmac\twifimac\tgaid\tgsfid\tfp\n' > "$OUT"
log(){ echo "$(date +%H:%M:%S) $*" | tee -a "$LOG"; }
gfield=""; [ "$GAPPS" = 1 ] && gfield=',"gapps":true'
imei_of(){ docker exec "pp-$1" sh -c "service call iphonesubinfo 1 s16 com.android.shell 2>/dev/null" | python3 -c 'import sys,re;s=sys.stdin.read();c=re.findall(chr(39)+"([^"+chr(39)+"]*)"+chr(39),s);print(re.sub(r"[^0-9]","","".join(c)))' 2>/dev/null; }
done=0; b=0
while [ "$done" -lt "$TOTAL" ]; do
  b=$((b+1)); n=$((TOTAL-done)); [ "$n" -gt "$BATCH" ] && n=$BATCH
  temp=$(( $(cat /sys/class/thermal/thermal_zone0/temp)/1000 ))
  [ "$temp" -gt 78 ] && { log "温度${temp}℃>78,等降温60s"; sleep 60; }
  log "批次$b: 建 $n 台 (累计 $done/$TOTAL, 温度 ${temp}℃)"
  ids=""
  for i in $(seq 1 "$n"); do
    body="{\"name\":\"idt-$tag-b$b-$i\",\"modelId\":\"poco-x4-gt\",\"androidVersion\":$VER,\"proxyId\":\"direct-dev\"$gfield}"
    id=$(curl -s -m20 -X POST -H "$H" -H 'Content-Type: application/json' -d "$body" "$API/phones" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id",""))' 2>/dev/null)
    if [ -n "$id" ]; then ids="$ids $id"; curl -s -m60 -X POST -H "$H" "$API/phones/$id/start" -o /dev/null; else log "  建手机失败: $body"; fi
  done
  # 等每台 boot_completed (最多 240s)
  for id in $ids; do
    for t in $(seq 1 24); do
      [ "$(docker exec pp-$id getprop sys.boot_completed 2>/dev/null)" = "1" ] && break
      sleep 10
    done
  done
  sleep 25   # 让管家把身份属性喂进去(androidId 等稍晚才settle)
  # 这批的 API fields 一次性拉下来(assigned bt/wifi/gaid/gsf)
  curl -s -H "$H" "$API/phones" > /mmc/pp-build/.batch.json 2>/dev/null
  for id in $ids; do
    C=pp-$id
    bc=$(docker exec $C getprop sys.boot_completed 2>/dev/null)
    mali=$(docker exec $C sh -c 'dumpsys SurfaceFlinger 2>/dev/null | grep -oE "Mali-G[0-9]+" | head -1'); [ -z "$mali" ] && mali=NONE
    sensors=$(docker exec $C sh -c 'dumpsys sensorservice 2>/dev/null | grep -icE "android.sensor"' 2>/dev/null)
    ril=$(docker exec $C sh -c 'dumpsys telephony.registry 2>/dev/null | grep -oE "mVoiceRegState=[0-9]\([A-Z_]*\)" | head -1')
    fatal=$(docker exec $C sh -c "logcat -d -t 600 2>/dev/null | grep -cE 'FATAL EXCEPTION IN SYSTEM|Failed to load frameworks'" 2>/dev/null)
    aid=$(docker exec $C sh -c 'settings get secure android_id 2>/dev/null')
    serial=$(docker exec $C getprop ro.serialno 2>/dev/null)
    bootid=$(docker exec $C sh -c 'cat /proc/sys/kernel/random/boot_id 2>/dev/null')
    imei=$(imei_of "$id")
    gsf=$(docker exec $C sh -c 'content query --uri content://com.google.android.gsf.gservices --where "name=\"android_id\"" 2>/dev/null | grep -oE "value=[0-9a-fA-F]+" | head -1')
    [ -z "$gsf" ] && gsf=NA
    fp=$(docker exec $C getprop ro.build.fingerprint 2>/dev/null)
    read bt wf gd < <(python3 -c "import json;d=json.load(open('/mmc/pp-build/.batch.json'));p=[x for x in d if x['id']=='$id'];f=p[0]['fields'] if p else {};print(f.get('bluetoothMac',{}).get('value','?'),f.get('wifiMac',{}).get('value','?'),f.get('gaid',{}).get('value','?'))" 2>/dev/null)
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$id" "$bc" "$mali" "$sensors" "$ril" "$fatal" "$aid" "$serial" "$bootid" "$imei" "$bt" "$wf" "$gd" "$gsf" "$fp" >> "$OUT"
  done
  for id in $ids; do
    docker kill pp-$id >/dev/null 2>&1
    curl -s -m60 -X DELETE -H "$H" "$API/phones/$id" -o /dev/null
  done
  done=$((done+n))
  log "批次$b 完成并删净, 累计 $done/$TOTAL, 温度 $(( $(cat /sys/class/thermal/thermal_zone0/temp)/1000 ))℃"
done
log "===== $tag 全部 $TOTAL 台跑完 ====="
