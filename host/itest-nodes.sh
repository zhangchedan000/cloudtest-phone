#!/usr/bin/env bash
# 节点 / 订阅实机测试（root，在仓库目录里，网络模块要开着）：
#   bash host/itest-nodes.sh
# 不需要真实机场：在本机临时起一个 mihomo 当「测试节点服务器」（只监听 127.0.0.1，自签证书），
# 提供 ss / vmess / vless+TLS / trojan / hysteria2 五种入站，然后：
#   订阅导入（分享链接 base64、Clash YAML、订阅地址下载、重复导入）→ 经 mihomo 逐个检测 →
#   连不上的节点要报失败 → 代理库编辑（改名保留检测结果、改地址清空）→ 有设备在用不能删 → 清理。
# 测试节点直接出网，所以检测出来的出口 IP 应该等于服务器自己的 IP。
set -u
BASE=${PP_BASE:-http://127.0.0.1:8099}
NOTE="itest-nodes $(date +%s)"
D=$(mktemp -d /tmp/pp-itest-nodes.XXXX)
pass=0; failn=0
ok()  { echo "✅ $*"; pass=$((pass+1)); }
bad() { echo "❌ $*"; failn=$((failn+1)); }
api() { curl -s -m 120 -X "$1" "$BASE/api$2" ${3:+-H 'Content-Type: application/json' -d "$3"} ${PP_TOKEN:+-H "Authorization: Bearer $PP_TOKEN"}; }
jget() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1" 2>/dev/null; }
mine() { api GET /proxies | python3 -c "import json,sys; [print(p['id'], p['name']) for p in json.load(sys.stdin) if p.get('note','')=='$NOTE']"; }

cleanup() {
  for id in $(mine | cut -d' ' -f1); do api DELETE "/proxies/$id" >/dev/null; done
  [ -f "$D/server.pid" ] && kill "$(cat "$D/server.pid")" 2>/dev/null
  [ -f "$D/http.pid" ] && kill "$(cat "$D/http.pid")" 2>/dev/null
  rm -rf "$D"
}
trap cleanup EXIT

[ "$(api GET /host | jget 'd.get("network")')" = True ] || { echo "网络模块没开（管家程序要加 -mihomo-api）"; exit 1; }
grep -q pp-check-in /opt/prism-phone/data/mihomo/config.yaml || { echo "mihomo 配置里没有检测入口（-check-port 为 0？）"; exit 1; }
host_ip=$(curl -s -m 10 https://ipinfo.io/ip)
echo "== 服务器出口 $host_ip"

# ---- 测试节点服务器 ----
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 1 -subj /CN=pp-test.local \
  -keyout "$D/key.pem" -out "$D/cert.pem" 2>/dev/null
U=$(cat /proc/sys/kernel/random/uuid); PW="pp$(head -c 6 /dev/urandom | od -An -tx1 | tr -d ' \n')"
cat > "$D/server.yaml" <<EOF
log-level: warning
mode: rule
rules: ["MATCH,DIRECT"]
listeners:
  - {name: ss-in, type: shadowsocks, listen: 127.0.0.1, port: 18101, cipher: aes-128-gcm, password: $PW}
  - {name: vmess-in, type: vmess, listen: 127.0.0.1, port: 18102, users: [{username: u1, uuid: $U, alterId: 0}]}
  - {name: vless-in, type: vless, listen: 127.0.0.1, port: 18104, users: [{username: u1, uuid: $U}], certificate: $D/cert.pem, private-key: $D/key.pem}
  - {name: trojan-in, type: trojan, listen: 127.0.0.1, port: 18105, users: [{username: u1, password: $PW}], certificate: $D/cert.pem, private-key: $D/key.pem}
  - {name: hy2-in, type: hysteria2, listen: 127.0.0.1, port: 18106, users: {u1: $PW}, certificate: $D/cert.pem, private-key: $D/key.pem}
EOF
/usr/local/bin/mihomo -d "$D" -f "$D/server.yaml" > "$D/server.log" 2>&1 &
echo $! > "$D/server.pid"; sleep 3
n=$(ss -lnt | grep -cE '127.0.0.1:1810[1-6] ')
[ "$n" -ge 4 ] && ok "测试节点服务器起来了（TCP 入站 $n 个 + hysteria2 UDP）" || { bad "测试节点服务器没起来：$(tail -5 "$D/server.log")"; exit 1; }

# ---- 1. 分享链接列表（base64），含一条不支持的 ----
SS=$(printf 'aes-128-gcm:%s' "$PW" | base64 -w0)
VM=$(printf '{"v":"2","ps":"测试-vmess","add":"127.0.0.1","port":"18102","id":"%s","aid":"0","net":"tcp","tls":""}' "$U" | base64 -w0)
cat > "$D/links.txt" <<EOF
ss://$SS@127.0.0.1:18101#测试-ss
vmess://$VM
vless://$U@127.0.0.1:18104?security=tls&sni=pp-test.local&allowInsecure=1&type=tcp#测试-vless-tls
trojan://$PW@127.0.0.1:18105?sni=pp-test.local&allowInsecure=1#测试-trojan
hysteria2://$PW@127.0.0.1:18106?sni=pp-test.local&insecure=1#测试-hysteria2
tuic://x@127.0.0.1:1#不支持的
EOF
B64=$(base64 -w0 "$D/links.txt")
body() { python3 -c 'import json,sys;print(json.dumps({sys.argv[1]:sys.argv[2],"note":sys.argv[3]}))' "$@"; }
r=$(api POST /proxies/import "$(body text "$B64" "$NOTE")")
[ "$(echo "$r" | jget 'd["added"]')" = 5 ] && [ "$(echo "$r" | jget 'len(d["skipped"])')" = 1 ] \
  && ok "导入 base64 分享链接：加了 5 个，跳过 1 个（$(echo "$r" | jget 'd["skipped"][0]')）" || bad "导入分享链接：$r"
r=$(api POST /proxies/import "$(body text "$B64" "$NOTE")")
[ "$(echo "$r" | jget 'd["added"]')" = 0 ] && [ "$(echo "$r" | jget 'd["duplicates"]')" = 5 ] && ok "重复导入：0 个新加，5 个重复跳过" || bad "重复导入：$r"

# ---- 2. Clash YAML（trojan 带 skip-cert-verify），含一个不支持的类型 ----
cat > "$D/clash.yaml" <<EOF
proxies:
  - {name: 测试-clash-trojan, type: trojan, server: 127.0.0.1, port: 18105, password: $PW, sni: pp-test.local, skip-cert-verify: true, udp: true}
  - {name: 测试-clash-不支持, type: snell, server: 127.0.0.1, port: 1, psk: x}
EOF
r=$(api POST /proxies/import "$(body text "$(cat "$D/clash.yaml")" "$NOTE")")
[ "$(echo "$r" | jget 'd["added"]')" = 1 ] && [ "$(echo "$r" | jget 'len(d["skipped"])')" = 1 ] && ok "导入 Clash YAML：加了 1 个，跳过 1 个" || bad "导入 Clash YAML：$r"

# ---- 3. 订阅地址（本机临时 HTTP 服务，只放一条新链接） ----
mkdir -p "$D/www"; echo "ss://$SS@127.0.0.1:18101#测试-订阅地址" | base64 -w0 > "$D/www/sub"
(cd "$D/www" && exec python3 -m http.server 18180 --bind 127.0.0.1 >/dev/null 2>&1) &
echo $! > "$D/http.pid"; sleep 1
r=$(api POST /proxies/import "$(body url "http://127.0.0.1:18180/sub" "$NOTE")")
[ "$(echo "$r" | jget 'd["added"]')" = 1 ] && ok "订阅地址下载导入：加了 1 个" || bad "订阅地址导入：$r"
r=$(api POST /proxies/import "$(body url "http://127.0.0.1:18180/nope" "$NOTE")")
echo "$r" | grep -q 404 && ok "订阅地址打不开会报错（$(echo "$r" | jget 'd["error"]')）" || bad "坏订阅地址：$r"

# ---- 4. 逐个检测 ----
while read -r id name; do
  [ "$name" = "测试-订阅地址" ] && continue
  r=$(api POST "/proxies/$id/check")
  if [ "$(echo "$r" | jget 'd.get("ok")')" = True ] && [ "$(echo "$r" | jget 'd.get("exitIp")')" = "$host_ip" ]; then
    ok "检测 $name：出口 $(echo "$r" | jget 'd["exitIp"], d.get("country",""), str(d.get("latencyMs",""))+" ms"')"
  else bad "检测 $name：$r"; fi
done < <(mine)

# ---- 5. 连不上的节点 ----
r=$(api POST /proxies/import "$(body text "ss://$SS@127.0.0.1:18199#测试-连不上" "$NOTE")")
dead=$(mine | awk '$2=="测试-连不上"{print $1}')
r=$(api POST "/proxies/$dead/check")
[ "$(echo "$r" | jget 'd.get("ok")')" = False ] && ok "连不上的节点检测失败：$(echo "$r" | jget 'd.get("error","")' | head -c 120)" || bad "连不上的节点：$r"

# ---- 6. 编辑 ----
tj=$(mine | awk '$2=="测试-trojan"{print $1}')
cur=$(api GET /proxies | python3 -c "import json,sys; print(json.dumps([p for p in json.load(sys.stdin) if p['id']=='$tj'][0]))")
upd() { python3 -c 'import json,sys;p=json.loads(sys.argv[1]);p.update(json.loads(sys.argv[2]));print(json.dumps(p))' "$cur" "$1"; }
r=$(api PUT "/proxies/$tj" "$(upd '{"name":"测试-trojan-改名"}')")
[ "$(echo "$r" | jget 'd["name"]')" = 测试-trojan-改名 ] && [ "$(echo "$r" | jget 'bool(d.get("check"))')" = True ] && ok "改名：名字变了，检测结果保留" || bad "改名：$r"
cur=$(echo "$r")
r=$(api PUT "/proxies/$tj" "$(upd "{\"address\":\"trojan://$PW@127.0.0.1:18105?sni=pp-test.local&allowInsecure=1#改地址\"}")")
[ "$(echo "$r" | jget 'd.get("check")')" = None ] && ok "改地址：上次检测结果清空" || bad "改地址：$r"
r=$(api PUT "/proxies/$tj" '{"name":" ","address":""}'); echo "$r" | grep -q "不能为空" && ok "名称地址为空会拒绝" || bad "空名称：$r"

# ---- 7. 有设备在用不能删 ----
PH=$(api POST /phones "{\"name\":\"节点测试（不开机）\",\"modelId\":\"poco-x4-gt\",\"androidVersion\":14,\"proxyId\":\"$tj\"}" | jget 'd["id"]')
r=$(api DELETE "/proxies/$tj"); echo "$r" | grep -q "还在使用中" && ok "有设备在用的代理不能删（$(echo "$r" | jget 'd["error"]')）" || bad "删在用的代理：$r"
api DELETE "/phones/$PH" >/dev/null
for t in $(api GET /trash | jget "' '.join(t['name'] for t in d if t['phoneId']=='$PH')"); do api DELETE "/trash/$t" >/dev/null; done
r=$(api DELETE "/proxies/$tj"); echo "$r" | grep -q '"ok":true' && ok "设备删了以后代理能删" || bad "删代理：$r"

# ---- 8. 清理 ----
for id in $(mine | cut -d' ' -f1); do api DELETE "/proxies/$id" >/dev/null; done
[ -z "$(mine)" ] && ok "测试节点已从代理库清掉" || bad "代理库没清干净：$(mine)"
grep -q "127.0.0.1:1810" /opt/prism-phone/data/mihomo/config.yaml 2>/dev/null && echo "   （mihomo 配置要等下次同步才去掉测试节点，不影响使用）"
echo "=== 通过 $pass 项，失败 $failn 项 ==="
