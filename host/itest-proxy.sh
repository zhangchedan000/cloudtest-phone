#!/usr/bin/env bash
# 代理实机测试（root，在仓库目录里，网络模块要已经开着）：
#   PP_TEST_PROXY='socks5://用户名:密码@地址:端口' bash host/itest-proxy.sh
# 把代理加进代理库 → 检测出口 → 开一台用这个代理的测试手机 → 手机里查出口 IP 是否等于代理出口（不是服务器 IP）→
# 删手机、删代理。代理账号只从环境变量读，不写进仓库、不留在代理库里。
set -u
BASE=http://127.0.0.1:8099
pass=0; failn=0
ok()  { echo "✅ $*"; pass=$((pass+1)); }
bad() { echo "❌ $*"; failn=$((failn+1)); }
api() { curl -s -m 600 -X "$1" "$BASE/api$2" ${3:+-H 'Content-Type: application/json' -d "$3"}; }
jget() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1" 2>/dev/null; }
[ -n "${PP_TEST_PROXY:-}" ] || { echo "要设置 PP_TEST_PROXY"; exit 1; }
[ "$(api GET /host | jget 'd.get("network")')" = True ] || { echo "网络模块没开"; exit 1; }

PX=$(python3 -c 'import json,os;print(json.dumps({"name":"测试代理（测完删除）","protocol":"socks5","address":os.environ["PP_TEST_PROXY"]}))' | curl -s -X POST "$BASE/api/proxies" -H 'Content-Type: application/json' -d @- | jget 'd["id"]')
[ -n "$PX" ] && ok "代理已加进代理库" || { bad "加代理失败"; exit 1; }
chk=$(api POST "/proxies/$PX/check")
exit_ip=$(echo "$chk" | jget 'd.get("exitIp","")')
[ "$(echo "$chk" | jget 'd.get("ok")')" = True ] && ok "代理检测：出口 $exit_ip $(echo "$chk" | jget 'd.get("country",""), d.get("city",""), d.get("timezone",""), str(d.get("latencyMs",""))+" ms"')" || bad "代理检测失败：$(echo "$chk" | jget 'd.get("error")')"
host_ip=$(curl -s -m 10 https://ipinfo.io/ip)

ID=$(api POST /phones "{\"name\":\"代理测试\",\"modelId\":\"poco-x4-gt\",\"androidVersion\":14,\"proxyId\":\"$PX\"}" | jget 'd["id"]')
C="pp-$ID"
r=$(api POST "/phones/$ID/start"); echo "$r" | grep -q '"ok":true' && ok "开机" || bad "开机：$r"
for _ in $(seq 1 60); do [ "$(api GET /phones | jget "[p['status'] for p in d if p['id']=='$ID'][0]")" = running ] && break; sleep 5; done
IP=$(api GET /phones | jget "[p['ip'] for p in d if p['id']=='$ID'][0]")
grep -q "SRC-IP-CIDR,$IP/32,px-$PX" /opt/prism-phone/data/mihomo/config.yaml && ok "这台手机的规则指向测试代理" || bad "规则不对"
tz=$(docker exec "$C" getprop persist.sys.timezone 2>/dev/null)
echo "   手机时区：$tz（跟随代理）"
for _ in 1 2 3; do
  body=$(docker exec "$C" sh -c 'printf "GET /ip HTTP/1.1\r\nHost: ipinfo.io\r\nConnection: close\r\n\r\n" | nc -w 30 ipinfo.io 80' 2>&1 | tail -1)
  [ -n "$body" ] && [ "$body" != "nc: Timeout" ] && break
done
if [ "$body" = "$exit_ip" ] && [ "$body" != "$host_ip" ]; then ok "手机出口 = 代理出口 $body（服务器是 $host_ip）"
else bad "手机出口 [$body]，代理出口 [$exit_ip]，服务器 [$host_ip]"; fi

api POST "/phones/$ID/stop" >/dev/null; api DELETE "/phones/$ID" >/dev/null
api DELETE "/proxies/$PX" | grep -q '"ok":true' && ok "已删除测试手机和测试代理" || bad "清理代理失败"
echo "=== 通过 $pass 项，失败 $failn 项 ==="
