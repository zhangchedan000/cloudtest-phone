#!/usr/bin/env bash
# 一键刷机实机测试（root，在仓库目录里）：装 Android 15 镜像 → 开一台 14 的手机、留个标记文件 →
# 刷成 15 → 查系统版本是 15、标记文件没了（数据清空）→ 删手机。
set -u
cd "$(dirname "$0")/.."
BASE=http://127.0.0.1:8099
PP_TOKEN=${PP_TOKEN:-$(sed -n 's/^PP_TOKEN=//p' /etc/prism-phone/agent.env 2>/dev/null)}  # 管理页设了口令时自动带上
ENVF=/etc/prism-phone/agent.env
IMG15=${PP_IMG15:-redroid/redroid:15.0.0_64only-latest}
pass=0; failn=0
ok()  { echo "✅ $*"; pass=$((pass+1)); }
bad() { echo "❌ $*"; failn=$((failn+1)); }
api() { curl -s -m 900 -X "$1" "$BASE/api$2" ${3:+-H 'Content-Type: application/json' -d "$3"} ${PP_TOKEN:+-H "Authorization: Bearer $PP_TOKEN"}; }
jget() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1" 2>/dev/null; }
wait_running() { for _ in $(seq 1 90); do [ "$(api GET /phones | jget "[p['status'] for p in d if p['id']=='$1'][0]")" = running ] && return 0; sleep 5; done; return 1; }

docker image inspect "$IMG15" >/dev/null 2>&1 || docker pull -q "$IMG15" >/dev/null
docker image inspect "$IMG15" >/dev/null 2>&1 && ok "Android 15 镜像：$IMG15" || { bad "拉不到 $IMG15"; exit 1; }
if ! grep -q -- "-images" "$ENVF"; then
  sed -i "s#^AGENT_ARGS=\(.*\)\$#AGENT_ARGS=\1 -images 14=redroid/redroid:14.0.0_64only-latest,15=$IMG15#" "$ENVF"
  systemctl restart prism-phone-agent
  for _ in $(seq 1 30); do curl -s -m 2 "$BASE/api/health" >/dev/null && break; sleep 2; done
fi
[ "$(api GET /host | jget 'sorted(d["androidVersions"])')" = "[14, 15]" ] && ok "管家程序认到 Android 14、15" || bad "管家程序的安卓版本：$(api GET /host | jget 'd["androidVersions"]')"

ID=$(api POST /phones '{"name":"刷机测试","modelId":"poco-x4-gt","androidVersion":14,"proxyId":"direct-dev"}' | jget 'd["id"]')
C="pp-$ID"
api POST "/phones/$ID/start" >/dev/null
wait_running "$ID" && ok "Android 14 开机" || bad "14 没开机"
v=$(docker exec "$C" getprop ro.build.version.release 2>/dev/null)
[ "$v" = 14 ] && ok "系统版本 $v" || bad "系统版本 $v"
docker exec "$C" sh -c 'echo flash-marker > /data/local/tmp/pp-marker' && ok "留了标记文件"

r=$(api POST "/phones/$ID/reset" '{"renew":false,"androidVersion":15}')
echo "$r" | grep -q '"ok":true' && ok "刷机命令" || bad "刷机：$r"
wait_running "$ID" && ok "Android 15 开机" || bad "15 没开机"
v=$(docker exec "$C" getprop ro.build.version.release 2>/dev/null)
[ "$v" = 15 ] && ok "系统版本 $v" || bad "系统版本 $v"
docker exec "$C" ls /data/local/tmp/pp-marker >/dev/null 2>&1 && bad "标记文件还在（数据没清空）" || ok "旧数据已清空"
[ "$(api GET /phones | jget "[p['androidVersion'] for p in d if p['id']=='$ID'][0]")" = 15 ] && ok "管理页里显示 Android 15" || bad "设备记录的版本没变"
ls /opt/prism-phone/data/trash 2>/dev/null | grep -q "$ID-reset" && ok "旧数据在回收站" || bad "回收站里没有旧数据"

api POST "/phones/$ID/stop" >/dev/null; api DELETE "/phones/$ID" >/dev/null
echo "=== 通过 $pass 项，失败 $failn 项 ==="
