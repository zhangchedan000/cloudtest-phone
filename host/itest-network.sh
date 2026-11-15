#!/usr/bin/env bash
# 网络模块实机测试（root，在仓库目录里）：装 mihomo → 管家程序开网络模块 → 开一台测试手机 →
# 加透明代理规则（3 分钟不确认自动撤销）→ 在手机里查 DNS 和出口 → 都通过才 confirm，否则立刻撤销。
#
# 开发机用「直连（仅开发）」：流量经 mihomo 再直连出去，出口 IP 应等于服务器自己的 IP。
# 只动容器网段，服务器自己的 SSH / DC 不受影响（防失联规矩）。
set -u
cd "$(dirname "$0")/.."
BASE=http://127.0.0.1:8099
PP_TOKEN=${PP_TOKEN:-$(sed -n 's/^PP_TOKEN=//p' /etc/prism-phone/agent.env 2>/dev/null)}  # 管理页设了口令时自动带上
ENVF=/etc/prism-phone/agent.env
pass=0; failn=0
ok()  { echo "✅ $*"; pass=$((pass+1)); }
bad() { echo "❌ $*"; failn=$((failn+1)); }
api() { curl -s -m 600 -X "$1" "$BASE/api$2" ${3:+-H 'Content-Type: application/json' -d "$3"} ${PP_TOKEN:+-H "Authorization: Bearer $PP_TOKEN"}; }
jget() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1" 2>/dev/null; }

echo "== 1. 安装 mihomo"
bash host/network.sh install && ok "mihomo 运行中：$(/usr/local/bin/mihomo -v | head -1 | cut -c1-60)" || { bad "mihomo 安装失败"; exit 1; }

echo "== 2. 管家程序开网络模块"
grep -q -- "-mihomo-api" "$ENVF" || sed -i 's#^AGENT_ARGS=\(.*\)$#AGENT_ARGS=\1 -mihomo-api 127.0.0.1:9090#' "$ENVF"
systemctl restart prism-phone-agent; sleep 4
[ "$(api GET /host | jget 'd.get("network")')" = True ] && ok "管家程序网络模块已开" || bad "管家程序网络模块没开"
cfg=/opt/prism-phone/data/mihomo/config.yaml
grep -q "MATCH,REJECT" "$cfg" 2>/dev/null && ok "配置已生成（$(grep -c SRC-IP-CIDR "$cfg") 条设备规则）" || bad "配置没生成"
curl -s -m 5 127.0.0.1:9090/configs | grep -q '"tproxy-port":7893' && ok "mihomo 已加载管家程序的配置" || bad "mihomo 没加载配置：$(journalctl -u prism-phone-mihomo -n 5 --no-pager | tail -3)"

echo "== 3. 开测试手机"
ID=$(api POST /phones '{"name":"网络测试","modelId":"poco-x4-gt","androidVersion":14,"proxyId":"direct-dev"}' | jget 'd["id"]')
C="pp-$ID"
r=$(api POST "/phones/$ID/start"); echo "$r" | grep -q '"ok":true' && ok "开机" || bad "开机：$r"
for _ in $(seq 1 60); do [ "$(api GET /phones | jget "[p['status'] for p in d if p['id']=='$ID'][0]")" = running ] && break; sleep 5; done
IP=$(api GET /phones | jget "[p['ip'] for p in d if p['id']=='$ID'][0]")
grep -q "SRC-IP-CIDR,$IP/32,DIRECT" "$cfg" && ok "配置里有这台手机的规则（$IP）" || bad "配置里没有 $IP 的规则"

echo "== 4. 加规则（3 分钟不确认自动撤销）"
bash host/network.sh apply && ok "规则已加" || bad "加规则失败"

echo "== 5. 手机里检查"
dns=$(docker exec "$C" ping -c 1 -W 3 ipinfo.io 2>&1 | head -1)
echo "$dns" | grep -q "198\.18\." && ok "DNS 经 mihomo（fake-ip）：$dns" || bad "DNS 没走 mihomo：$dns"
host_ip=$(curl -s -m 10 https://ipinfo.io/ip)
# 第一次请求要等安卓的加密 DNS（DoT）建好连接，比较慢：给 30 秒、最多试 3 次
for _ in 1 2 3; do
  body=$(docker exec "$C" sh -c 'printf "GET /ip HTTP/1.1\r\nHost: ipinfo.io\r\nConnection: close\r\n\r\n" | nc -w 30 ipinfo.io 80' 2>&1 | tail -1)
  [ "$body" = "$host_ip" ] && break
done
[ -n "$host_ip" ] && [ "$body" = "$host_ip" ] && ok "出口经 mihomo 直连，IP = 服务器 IP $host_ip" || bad "出口不对：手机 [$body] 服务器 [$host_ip]"
# 出口 IP 和服务器一样（开发机直连），光看 IP 分不出走没走 mihomo；开一条慢连接，在 mihomo 的连接列表里找这台手机。
docker exec "$C" sh -c '(sleep 8; printf "GET / HTTP/1.0\r\n\r\n") | nc -w 12 example.com 80' >/dev/null 2>&1 &
sleep 4
curl -s -m 5 127.0.0.1:9090/connections | grep -q "\"sourceIP\":\"$IP\"" && ok "mihomo 里能看到这台手机的连接（流量确实经过网络模块）" || bad "mihomo 里看不到这台手机的连接（流量没经过网络模块）"
wait
ssh_ok=$(ss -ltn | grep -c ':22 ')
[ "$ssh_ok" -ge 1 ] && ok "服务器 SSH 照常监听" || bad "SSH 没在监听"

echo "== 6. 结论"
if [ "$failn" = 0 ]; then
  bash host/network.sh confirm && ok "全部通过，已确认规则（开机自动加）"
else
  bash host/network.sh rollback
  # 规则撤了，网络模块也要关：否则管家程序以为有保护，实际手机经 Docker NAT 直接用服务器网络
  sed -i 's# -mihomo-api 127.0.0.1:9090##' "$ENVF"; systemctl restart prism-phone-agent
  echo "有失败，已撤销规则并关掉网络模块（回到开发模式）"
fi
# 管家程序可能刚重启，等它起来再清理测试手机
for _ in $(seq 1 30); do curl -s -m 2 "$BASE/api/health" >/dev/null && break; sleep 2; done
api POST "/phones/$ID/stop" >/dev/null; api DELETE "/phones/$ID" >/dev/null
echo "=== 通过 $pass 项，失败 $failn 项 ==="
