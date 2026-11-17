#!/usr/bin/env bash
# 网络链路体检（root，在仓库目录里，网络模块要开着）：对正在运行的云手机逐台查，看外面看到的是不是节点、有没有漏。
#
#   bash host/check-link.sh                    # 所有运行中的手机（只读，不改任何东西）
#   bash host/check-link.sh <手机ID>...        # 指定几台
#   bash host/check-link.sh --switch <手机ID>  # 另外做「切换节点」和「节点断了」两项主动测试（会临时换这台的节点，测完换回）
#
# 每台查：出口 IP = 节点出口、DNS 走节点（外面看到的解析来源不是本机）、UDP（WebRTC 用的 STUN）不漏本机 IP、
# 没有 IPv6 绕路、时区和出口国家一致、延迟和下载速度；网络模块规则里这台走对的节点、兜底是拒绝（REJECT）不是直连。
# 测试流量从宿主机进到手机的网络命名空间里发（nsenter -n），和手机里 App 走的是同一条路（wlan0 → 路由器 → 透明代理 → 节点）。
set -u
[ "$(id -u)" = 0 ] || { echo "请用 root 运行" >&2; exit 1; }
API=${PP_API:-http://127.0.0.1:8099/api}
TOKEN=${PP_TOKEN:-$(sed -n 's/^PP_TOKEN=//p' /etc/prism-phone/agent.env 2>/dev/null)}
MIHOMO=${PP_MIHOMO:-127.0.0.1:9090}
CONF=${PP_MIHOMO_CONF:-/opt/prism-phone/data/mihomo/config.yaml}
api() { curl -s -m 90 -X "$1" "$API$2" ${3:+-H 'Content-Type: application/json' -d "$3"} ${TOKEN:+-H "Authorization: Bearer $TOKEN"}; }
pass=0; fail=0; warn=0
ok()   { echo "  ✓ $*"; pass=$((pass+1)); }
bad()  { echo "  ✗ $*"; fail=$((fail+1)); }
note() { echo "  ! $*"; warn=$((warn+1)); }
W=$(mktemp -d); trap 'rm -rf "$W"' EXIT

# 手机网络命名空间里跑的小工具（Python 标准库）：DNS 查询（A / AAAA / TXT）、STUN 拿 UDP 映射地址
cat > "$W/net.py" <<'PY'
import random, socket, struct, sys

def dns(server, name, qtype):
    t = {"A": 1, "AAAA": 28, "TXT": 16}[qtype]
    qid = random.randint(0, 65535)
    q = struct.pack(">HHHHHH", qid, 0x0100, 1, 0, 0, 0) + b"".join(bytes([len(p)]) + p.encode() for p in name.split(".")) + b"\0" + struct.pack(">HH", t, 1)
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(6)
    s.sendto(q, (server, 53)); r, _ = s.recvfrom(4096)
    an = struct.unpack(">H", r[6:8])[0]; i = 12
    while r[i]: i += r[i] + 1
    i += 5
    out = []
    for _ in range(an):
        if r[i] & 0xC0 == 0xC0: i += 2
        else:
            while r[i]: i += r[i] + 1
            i += 1
        typ, _, _, ln = struct.unpack(">HHIH", r[i:i+10]); i += 10; d = r[i:i+ln]; i += ln
        if typ == 1: out.append(socket.inet_ntoa(d))
        elif typ == 28: out.append(socket.inet_ntop(socket.AF_INET6, d))
        elif typ == 16: out.append(d[1:1+d[0]].decode(errors="replace"))
    return out

def stun(host_ip, port=19302):
    tid = random.randbytes(12) if hasattr(random, "randbytes") else bytes(random.getrandbits(8) for _ in range(12))
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(6)
    s.sendto(struct.pack(">HHI", 1, 0, 0x2112A442) + tid, (host_ip, port))
    r, _ = s.recvfrom(2048); i = 20
    while i < len(r):
        typ, ln = struct.unpack(">HH", r[i:i+4]); v = r[i+4:i+4+ln]
        if typ == 0x0020:  # XOR-MAPPED-ADDRESS
            return socket.inet_ntoa(bytes(b ^ m for b, m in zip(v[4:8], b"\x21\x12\xa4\x42")))
        i += 4 + ln + (-ln % 4)
    return ""

cmd = sys.argv[1]
try:
    if cmd == "dns": print(" ".join(dns(sys.argv[2], sys.argv[3], sys.argv[4])))
    elif cmd == "stun": print(stun(sys.argv[2]))
except Exception as e:
    print("ERR " + type(e).__name__)
PY

host_ip=$(curl -s -m 10 https://api.ipify.org)
echo "服务器自己的出口 IP：${host_ip:-（查不到）}"
phones=$(api GET /phones)
proxies=$(api GET /proxies)
[ -n "$phones" ] || { echo "连不上管家程序 $API"; exit 1; }
field() { python3 -c "import json,sys; d=json.loads(sys.argv[1]); p=[x for x in d if x['id']==sys.argv[2]]; v=p[0] if p else {}
for k in sys.argv[3].split('.'): v=(v or {}).get(k) if isinstance(v,dict) else None
print('' if v is None else v)" "$1" "$2" "$3"; }

switch=0; ids=()
for a in "$@"; do case "$a" in --switch) switch=1 ;; *) ids+=("$a") ;; esac; done
if [ ${#ids[@]} -eq 0 ]; then
  mapfile -t ids < <(python3 -c "import json,sys; [print(p['id']) for p in json.loads(sys.argv[1]) if p.get('status')=='running']" "$phones")
fi
[ ${#ids[@]} -gt 0 ] || { echo "没有运行中的手机"; exit 0; }

# 规则兜底：没有代理的设备一律拒绝，绝不走服务器自己的网络
grep -q 'MATCH,REJECT' "$CONF" && ok "网络模块兜底规则是 REJECT（节点不通 / 没分配就断网，不会退回服务器网络）" || bad "网络模块配置里没有 MATCH,REJECT 兜底"

check_phone() {
  local id=$1 C="pp-$1" pid name pxid pxname want_ip want_tz ip gw
  name=$(field "$phones" "$id" name); pxid=$(field "$phones" "$id" proxyId)
  pxname=$(field "$proxies" "$pxid" name); want_ip=$(field "$proxies" "$pxid" check.exitIp); want_tz=$(field "$proxies" "$pxid" check.timezone)
  [ "$pxid" = direct-dev ] && { pxname="直连（仅开发）"; want_ip=$host_ip; }
  echo; echo "== $name（$id）节点：${pxname:-$pxid}  节点检测出口：${want_ip:-未检测}"
  pid=$(docker inspect -f '{{.State.Pid}}' "$C" 2>/dev/null)
  [ -n "$pid" ] && [ "$pid" -gt 0 ] || { bad "容器没在运行"; return; }
  ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$C")
  nx() { nsenter -t "$pid" -n "$@"; }
  # 安卓用策略路由（默认路由不在主表里）：问内核去外网走哪
  route=$(nx ip route get 1.1.1.1 2>/dev/null | head -1)
  gw=$(echo "$route" | sed -n 's/.* via \([0-9.]*\).*/\1/p'); dev=$(echo "$route" | sed -n 's/.* dev \([^ ]*\).*/\1/p')
  [ "$dev" = wlan0 ] && ok "手机走 WiFi（wlan0，网关 $gw）" || note "默认路由走 ${dev:-?}（不是 WiFi）"

  # 规则：这台手机的流量走哪个出口
  rule=$(grep -E "SRC-IP-CIDR,$ip/32," "$CONF" | head -1 | awk -F, '{print $3}')
  if [ "$pxid" = direct-dev ]; then [ "$rule" = DIRECT ] && ok "规则：直连（开发机）" || bad "规则不对：${rule:-没有}"
  else [ "$rule" = "px-$pxid" ] && ok "规则：走节点 $pxname" || bad "规则不对：${rule:-没有}（应为 px-$pxid）"; fi

  # DNS：解析得到、而且外面看到的解析来源不是服务器
  a=$(nx python3 -I "$W/net.py" dns "$gw" api.ipify.org A)
  case "$a" in ""|ERR*) bad "DNS 解析失败（问网关 $gw：${a:-无结果}）"; return ;; *) ok "DNS 能解析（api.ipify.org → ${a%% *}）" ;; esac
  res=$(nx python3 -I "$W/net.py" dns "$gw" o-o.myaddr.l.google.com TXT)
  if [ "$pxid" != direct-dev ] && echo "$res" | grep -qw "$host_ip"; then bad "DNS 泄露：谷歌看到的解析来源是服务器 IP（$res）"
  else ok "DNS 不泄露服务器 IP（谷歌看到的解析来源：${res:-无}）"; fi

  # 出口 IP（HTTP）
  t0=$(date +%s%N)
  exit_ip=$(nx curl -s -m 20 --resolve "api.ipify.org:443:${a%% *}" https://api.ipify.org)
  ms=$(( ($(date +%s%N) - t0) / 1000000 ))
  if [ -z "$exit_ip" ]; then bad "上不了网（api.ipify.org 没回）"
  elif [ "$pxid" != direct-dev ] && [ "$exit_ip" = "$host_ip" ]; then bad "出口是服务器自己的 IP $exit_ip（没走节点！）"
  elif [ -n "$want_ip" ] && [ "$exit_ip" != "$want_ip" ]; then note "出口 $exit_ip 和节点检测时的 $want_ip 不一样（机场轮换出口 IP 时会这样）"
  else ok "出口 IP = $exit_ip（${ms}ms）"; fi

  # UDP：WebRTC 用 STUN 拿公网地址，不能是服务器 IP
  sip=$(nx python3 -I "$W/net.py" dns "$gw" stun.l.google.com A | awk '{print $1}')
  m=$(nx python3 -I "$W/net.py" stun "${sip:-74.125.250.129}")
  case "$m" in
    ""|ERR*) note "UDP（STUN）不通：节点可能不支持 UDP——视频通话、QUIC 会退回 TCP，但不漏 IP" ;;
    "$host_ip") [ "$pxid" = direct-dev ] && ok "UDP 映射地址 = 服务器（直连）" || bad "WebRTC 泄露：UDP 映射地址是服务器 IP $m" ;;
    *) ok "UDP（STUN）映射地址 = $m（不漏服务器 IP）" ;;
  esac

  # IPv6：不能绕过代理
  if nx ip -6 route show default 2>/dev/null | grep -q default; then
    v6=$(nx curl -6 -s -m 8 https://api6.ipify.org 2>/dev/null)
    [ -n "$v6" ] && bad "IPv6 绕过代理直接出去了（$v6）" || ok "有 IPv6 路由但出不去（不绕路）"
  else ok "没有 IPv6 默认路由（不会绕过代理）"; fi

  # 时区和出口国家
  tz=$(docker exec "$C" getprop persist.sys.timezone 2>/dev/null)
  if [ -n "$want_tz" ]; then [ "$tz" = "$want_tz" ] && ok "时区 $tz 和节点一致" || bad "时区 $tz ≠ 节点 $want_tz"
  else note "节点没检测过，时区（$tz）没法比"; fi

  # 速度：首包延迟 + 下 5MB
  ip_cf=$(nx python3 -I "$W/net.py" dns "$gw" speed.cloudflare.com A | awk '{print $1}')
  if [ -n "$ip_cf" ] && [ "${ip_cf#ERR}" = "$ip_cf" ]; then
    r=$(nx curl -s -o /dev/null -m 60 --resolve "speed.cloudflare.com:443:$ip_cf" -w '%{time_starttransfer} %{speed_download}' "https://speed.cloudflare.com/__down?bytes=5000000")
    python3 -c "import sys; t,s=sys.argv[1].split(); print('  · 首包 %.0fms，下载 %.1f Mbit/s' % (float(t)*1000, float(s)*8/1e6))" "${r:-0 0}"
  fi
}

for id in "${ids[@]}"; do check_phone "$id"; done

# 主动测试：切换节点（老连接要断、新连接走新节点）、节点断了（要断网，不能直连）
if [ $switch = 1 ]; then
  id=${ids[0]}; C="pp-$id"
  pid=$(docker inspect -f '{{.State.Pid}}' "$C"); ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$C")
  orig=$(field "$phones" "$id" proxyId)
  echo; echo "== 主动测试（$id，原节点 $orig，测完换回）"
  conns() { curl -s -m 5 "http://$MIHOMO/connections" | python3 -c "import json,sys
cs=[c for c in json.load(sys.stdin).get('connections') or [] if c['metadata'].get('sourceIP')=='$ip']
print(len(cs), sum(1 for c in cs if 'px-$1' in c.get('chains',[])))"; }
  # 另一个检测通过的节点
  other=$(python3 -c "import json,sys; d=json.loads(sys.argv[1]); print(next((p['id'] for p in d if p['id'] not in (sys.argv[2],'direct-dev') and (p.get('check') or {}).get('ok')), ''))" "$proxies" "$orig")
  if [ -z "$other" ]; then note "没有别的检测通过的节点，跳过切换测试"
  else
    read -r n_before _ < <(conns "$orig")
    api POST "/phones/$id/proxy" "{\"proxyId\":\"$other\"}" >/dev/null
    sleep 2
    read -r n_after n_old < <(conns "$orig")
    [ "${n_old:-1}" = 0 ] && ok "切换后老节点上的连接都断了（切换前 $n_before 条）" || bad "切换后还有 $n_old 条连接挂在老节点上"
    gw=$(nsenter -t "$pid" -n ip route get 1.1.1.1 | sed -n 's/.* via \([0-9.]*\).*/\1/p')
    a=$(nsenter -t "$pid" -n python3 -I "$W/net.py" dns "$gw" api.ipify.org A | awk '{print $1}')
    e=$(nsenter -t "$pid" -n curl -s -m 20 --resolve "api.ipify.org:443:$a" https://api.ipify.org)
    w=$(field "$proxies" "$other" check.exitIp)
    [ -n "$e" ] && [ "$e" != "$host_ip" ] && ok "切到新节点后出口 = $e（新节点检测出口 $w）" || bad "切到新节点后出口不对：${e:-没网}"
  fi
  # 节点断了：临时加一个连不上的节点，切过去，应该完全没网（不能退回服务器网络）
  dead=$(api POST /proxies '{"name":"链路体检-坏节点","protocol":"socks5","address":"socks5://127.0.0.1:9","note":"check-link 临时"}' | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)
  if [ -n "$dead" ]; then
    api POST "/phones/$id/proxy" "{\"proxyId\":\"$dead\"}" >/dev/null; sleep 3
    gw=$(nsenter -t "$pid" -n ip route get 1.1.1.1 | sed -n 's/.* via \([0-9.]*\).*/\1/p')
    a=$(nsenter -t "$pid" -n python3 -I "$W/net.py" dns "$gw" api.ipify.org A | awk '{print $1}')
    e=$(nsenter -t "$pid" -n curl -s -m 15 --resolve "api.ipify.org:443:${a:-104.26.12.205}" https://api.ipify.org)
    [ -z "$e" ] && ok "节点断了 → 手机断网（没有退回服务器网络）" || bad "节点断了手机还能上网，出口 $e（应该断网！）"
  else note "没建成临时坏节点，跳过断网测试"; fi
  # 换回原节点，删掉临时节点（手机挂的节点列表也还原）
  api POST "/phones/$id/proxy" "{\"proxyId\":\"$orig\"}" >/dev/null
  list=$(field "$phones" "$id" proxyIds)
  [ -n "$list" ] && api PUT "/phones/$id" "{\"proxyIds\":$(python3 -c "import ast,json,sys; print(json.dumps(ast.literal_eval(sys.argv[1])))" "$list")}" >/dev/null
  [ -n "$dead" ] && api DELETE "/proxies/$dead" >/dev/null
  ok "已换回原节点 $orig、删掉临时节点"
fi

echo; echo "结果：通过 $pass 项，问题 $fail 项，提醒 $warn 项"
[ $fail = 0 ]
