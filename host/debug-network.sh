#!/usr/bin/env bash
# 网络模块排查（root，在仓库目录里）：开一台测试手机 → 加规则（3 分钟自动撤销）→ mihomo 临时开详细日志 →
# 手机里请求两个网站 → 记下 mihomo 的连接详情和日志 → 撤销规则、删手机。结果写到标准输出。
set -u
cd "$(dirname "$0")/.."
BASE=http://127.0.0.1:8099
ENVF=/etc/prism-phone/agent.env
api() { curl -s -m 600 -X "$1" "$BASE/api$2" ${3:+-H 'Content-Type: application/json' -d "$3"}; }
jget() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1" 2>/dev/null; }

grep -q -- "-mihomo-api" "$ENVF" || sed -i 's#^AGENT_ARGS=\(.*\)$#AGENT_ARGS=\1 -mihomo-api 127.0.0.1:9090#' "$ENVF"
systemctl restart prism-phone-agent
for _ in $(seq 1 30); do curl -s -m 2 "$BASE/api/health" >/dev/null && break; sleep 2; done
ID=$(api POST /phones '{"name":"网络排查","modelId":"poco-x4-gt","androidVersion":14,"proxyId":"direct-dev"}' | jget 'd["id"]')
C="pp-$ID"
api POST "/phones/$ID/start" >/dev/null
for _ in $(seq 1 60); do [ "$(api GET /phones | jget "[p['status'] for p in d if p['id']=='$ID'][0]")" = running ] && break; sleep 5; done
IP=$(api GET /phones | jget "[p['ip'] for p in d if p['id']=='$ID'][0]")
echo "== 手机 $ID IP $IP"
bash host/network.sh apply
curl -s -m 5 -X PATCH 127.0.0.1:9090/configs -H 'Content-Type: application/json' -d '{"log-level":"debug"}' >/dev/null
since=$(date '+%F %T')

echo "== 手机里的路由和 DNS"
docker exec "$C" ip route 2>&1 | head -5
docker exec "$C" getprop net.dns1 2>&1

for host in example.com ipinfo.io; do
  echo "== 手机请求 $host（30 秒超时）"
  docker exec "$C" sh -c "printf 'GET / HTTP/1.0\r\nHost: $host\r\n\r\n' | nc -w 30 $host 80" 2>&1 | head -3 &
  sleep 3
  curl -s -m 5 127.0.0.1:9090/connections | python3 -c "
import json,sys
d=json.load(sys.stdin)
for c in d.get('connections') or []:
    m=c['metadata']
    if m.get('sourceIP')=='$IP':
        print('  连接', m.get('network'), m.get('host') or m.get('destinationIP'), m.get('destinationPort'), '规则', c.get('rule'), c.get('rulePayload'), '链', c.get('chains'), '上传', c.get('upload'), '下载', c.get('download'))
" 2>&1
  wait
done

echo "== 宿主机直接请求（对照）"
curl -s -m 10 -o /dev/null -w "example.com %{http_code}\n" http://example.com

echo "== mihomo 详细日志（本次）"
journalctl -u prism-phone-mihomo --no-pager --since "$since" | cut -c60- | grep -vE "configuration|Geo|Sniffer" | tail -40

echo "== 收尾"
curl -s -m 5 -X PATCH 127.0.0.1:9090/configs -H 'Content-Type: application/json' -d '{"log-level":"warning"}' >/dev/null
bash host/network.sh rollback
sed -i 's# -mihomo-api 127.0.0.1:9090##' "$ENVF"; systemctl restart prism-phone-agent
for _ in $(seq 1 30); do curl -s -m 2 "$BASE/api/health" >/dev/null && break; sleep 2; done
api POST "/phones/$ID/stop" >/dev/null; api DELETE "/phones/$ID" >/dev/null
echo "== 完成（规则已撤销，网络模块已关）"
