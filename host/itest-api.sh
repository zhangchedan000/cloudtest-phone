#!/usr/bin/env bash
# 实机测试：通过管家程序的接口走一遍常用功能。在跑着管家程序的机器上执行（root）。
#
# 用法： bash host/itest-api.sh [管家程序地址，默认 http://127.0.0.1:8099] [APK 路径（可选）]
# 每一项打 ✅ / ❌；最后汇总。会新建一台叫「接口测试」的云手机，测完删除（数据进回收站）。
# 开发机用「直连（仅开发）」，不需要代理。
set -u
BASE=${1:-http://127.0.0.1:8099}
APK=${2:-}
TMP=$(mktemp -d)
pass=0; failn=0
ok()  { echo "✅ $*"; pass=$((pass+1)); }
bad() { echo "❌ $*"; failn=$((failn+1)); }
api() { curl -s -m 600 -X "$1" "$BASE/api$2" ${3:+-H 'Content-Type: application/json' -d "$3"} ${PP_TOKEN:+-H "Authorization: Bearer $PP_TOKEN"}; }
jget() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1" 2>/dev/null; }

echo "== 管家程序：$BASE"
api GET /host | jget 'd["name"], "网络模块:", d.get("network")' || { bad "连不上管家程序"; exit 1; }

ID=$(api POST /phones '{"name":"接口测试","modelId":"poco-x4-gt","androidVersion":14,"proxyId":"direct-dev"}' | jget 'd["id"]')
[ -n "$ID" ] && ok "创建 $ID" || { bad "创建失败"; exit 1; }
api PUT "/phones/$ID" '{"fields":{"timezone":{"mode":"manual","value":"America/Chicago"},"locale":{"mode":"manual","value":"en-US"}}}' >/dev/null
C="pp-$ID"

r=$(api GET "/phones/$ID/screen"); echo "$r" | grep -q "没有开机" && ok "关机时截屏会拒绝" || bad "关机时截屏：$r"

t0=$(date +%s)
r=$(api POST "/phones/$ID/start"); echo "$r" | grep -q '"ok":true' && ok "开机命令（$(( $(date +%s)-t0 )) 秒）" || bad "开机：$r"
st=""
for _ in $(seq 1 60); do
  st=$(api GET /phones | jget "[p['status'] for p in d if p['id']=='$ID'][0]")
  [ "$st" = running ] && break
  [ -z "${seen_starting:-}" ] && [ "$st" = starting ] && { ok "开机中显示为 starting"; seen_starting=1; }
  r=$(api POST "/phones/$ID/input" '{"kind":"key","key":"home"}')
  [ "$st" = starting ] && [ -z "${seen_wait:-}" ] && { echo "$r" | grep -q "还在开机" && { ok "开机中操作会提示等开机"; seen_wait=1; }; }
  sleep 5
done
[ "$st" = running ] && ok "开机完成（$(( $(date +%s)-t0 )) 秒）" || bad "等了 5 分钟还没开机完成（状态 $st）"

curl -s -m 60 -o "$TMP/s1.png" "$BASE/api/phones/$ID/screen" ${PP_TOKEN:+-H "Authorization: Bearer $PP_TOKEN"}
file -b "$TMP/s1.png" | grep -q PNG && ok "截屏：$(file -b "$TMP/s1.png" | cut -d, -f2)" || bad "截屏失败"

for body in '{"kind":"key","key":"home"}' '{"kind":"tap","x":540,"y":1200}' '{"kind":"swipe","x":540,"y":2000,"x2":540,"y2":600,"durationMs":300}' '{"kind":"key","key":"back"}' '{"kind":"text","text":"hello world"}'; do
  r=$(api POST "/phones/$ID/input" "$body"); echo "$r" | grep -q '"ok":true' && ok "操作 $body" || bad "操作 $body：$r"
done
r=$(api POST "/phones/$ID/input" '{"kind":"text","text":"你好"}'); echo "$r" | grep -q "只能输入英文" && ok "中文输入会拒绝" || bad "中文输入：$r"

# 开机后设置：语言变了会重启一次安卓框架，等它回来
for _ in $(seq 1 24); do
  s=$(api GET /phones | jget "[p.get('settings',{}).get('state') for p in d if p['id']=='$ID'][0]")
  [ "$s" = applied ] || [ "$s" = failed ] && break; sleep 5
done
[ "$s" = applied ] && ok "开机后设置：$s" || bad "开机后设置：$s $(api GET /phones | jget "[p.get('settings') for p in d if p['id']=='$ID'][0]")"
sleep 20
tz=$(docker exec "$C" getprop persist.sys.timezone 2>/dev/null); lc=$(docker exec "$C" getprop persist.sys.locale 2>/dev/null)
[ "$tz" = America/Chicago ] && ok "时区属性 $tz（date：$(docker exec "$C" date +%Z 2>/dev/null)）" || bad "时区属性：$tz"
[ "$lc" = en-US ] && ok "语言属性 $lc" || bad "语言属性：$lc"
for _ in $(seq 1 24); do [ "$(docker exec "$C" getprop sys.boot_completed 2>/dev/null)" = 1 ] && break; sleep 5; done

python3 -c "import base64;open('$TMP/t.png','wb').write(base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=='))"
r=$(curl -s -m 120 -F "file=@$TMP/t.png;type=image/png" "$BASE/api/phones/$ID/files?dir=DCIM" ${PP_TOKEN:+-H "Authorization: Bearer $PP_TOKEN"})
echo "$r" | grep -q '"ok":true' && docker exec "$C" ls /sdcard/DCIM/Camera/t.png >/dev/null 2>&1 && ok "上传图片到相册" || bad "上传：$r"
if [ -n "$APK" ] && [ -f "$APK" ]; then
  r=$(curl -s -m 600 -F "file=@$APK" "$BASE/api/phones/$ID/apk" ${PP_TOKEN:+-H "Authorization: Bearer $PP_TOKEN"})
  echo "$r" | grep -q '"ok":true' && ok "安装 APK $(basename "$APK")" || bad "安装 APK：$r"
else
  echo "（没给 APK，跳过安装测试）"
fi
r=$(api GET "/phones/$ID/logs?level=E&lines=20"); [ -n "$r" ] && ! echo "$r" | grep -q '"error"' && ok "看日志（$(echo "$r" | wc -l) 行）" || bad "日志：$r"

r=$(api POST "/phones/$ID/reset" '{"renew":false}'); echo "$r" | grep -q '"ok":true' && ok "一键重置" || bad "重置：$r"
r=$(api POST "/phones/$ID/stop"); echo "$r" | grep -q '"ok":true' && ok "关机" || bad "关机：$r"
r=$(api DELETE "/phones/$ID"); echo "$r" | grep -q '"ok":true' && ok "删除（数据进回收站）" || bad "删除：$r"
rm -rf "$TMP"
echo "=== 通过 $pass 项，失败 $failn 项 ==="
