#!/usr/bin/env bash
# 网络模块：让每台云手机的流量走它自己的代理（mihomo 透明代理），没有出口的一律拦截。
#
# 只动容器网段（默认 10.88.0.0/16、Docker 网络 pp-net 的网桥）：宿主机自己的 SSH、Desktop Commander、
# keepalive 都不经过这里（防失联规矩）。规则放在单独的 nftables 表 prism_phone 里，删掉这张表就完全恢复。
#
# 用法（root）：
#   bash host/network.sh install    # 下载并安装 mihomo（固定版本），建 systemd 服务（不改任何网络规则）
#   bash host/network.sh apply      # 加透明代理规则；3 分钟内不 confirm 就自动撤销
#   bash host/network.sh confirm    # 确认规则没问题，取消自动撤销
#   bash host/network.sh rollback   # 撤销规则（删除 prism_phone 表和策略路由）
#   bash host/network.sh status     # 看现状
#   bash host/network.sh heal       # 策略路由被别的程序删了就补回（自动更新每 10 分钟调一次，踩坑 #34）
#
# 状态：阿什本已实机启用（11-15）。改动前按「防失联规矩」：先确认甲骨文串口控制台能用。
set -euo pipefail

SUBNET=${PP_SUBNET:-10.88.0.0/16}
GATEWAY=${PP_GATEWAY:-10.88.0.1}
DOCKER_NET=${PP_DOCKER_NET:-pp-net}
TPROXY_PORT=${PP_TPROXY_PORT:-7893}
DNS_PORT=${PP_DNS_PORT:-1053}
MARK=0x1b5          # 只用于被透明代理的容器流量
TABLE=233           # 策略路由表号（不和系统已有的冲突）
MIHOMO_VERSION=${PP_MIHOMO_VERSION:-v1.19.34}
DATA=${PP_DATA:-/opt/prism-phone/data}
CONF="$DATA/mihomo/config.yaml"

log() { printf '[network] %s\n' "$*"; }
die() { printf '[network] 错误：%s\n' "$*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die "请用 root 运行"

bridge() {
  # 管家程序建网络时指定了网桥名（com.docker.network.bridge.name，默认就叫 pp-net）；
  # 没指定时 Docker 用 br-<网络 ID 前 12 位>（踩坑 #20：第一版写死了后者，规则一直没匹配上）
  local name id
  name=$(docker network inspect -f '{{index .Options "com.docker.network.bridge.name"}}' "$DOCKER_NET" 2>/dev/null) || true
  [ "$name" = "<no value>" ] && name=""
  if [ -z "$name" ]; then
    id=$(docker network inspect -f '{{.Id}}' "$DOCKER_NET" 2>/dev/null | cut -c1-12) || true
    [ -n "$id" ] || die "Docker 网络 $DOCKER_NET 不存在（先用管家程序开一次机，它会建网络）"
    name="br-$id"
  fi
  ip link show "$name" >/dev/null 2>&1 || die "网桥 $name 不存在"
  echo "$name"
}

install_mihomo() {
  command -v nft >/dev/null || { apt-get install -y nftables >/dev/null; }
  if [ ! -x /usr/local/bin/mihomo ] || ! /usr/local/bin/mihomo -v 2>/dev/null | grep -q "$MIHOMO_VERSION"; then
    local tmp url
    tmp=$(mktemp -d)
    url="https://github.com/MetaCubeX/mihomo/releases/download/$MIHOMO_VERSION/mihomo-linux-arm64-$MIHOMO_VERSION.gz"
    log "下载 $url"
    curl -fsSL --retry 3 -o "$tmp/mihomo.gz" "$url"
    log "sha256：$(sha256sum "$tmp/mihomo.gz" | cut -d' ' -f1)（第一次装时记进 docs/踩坑记录.md，以后核对）"
    gunzip "$tmp/mihomo.gz"
    install -m 0755 "$tmp/mihomo" /usr/local/bin/mihomo
    rm -rf "$tmp"
  fi
  mkdir -p "$DATA/mihomo"
  chmod 700 "$DATA/mihomo"
  # 管家程序会生成真正的配置；这里先放一个「全部拒绝」的，保证 mihomo 起得来又不放行任何流量。
  [ -f "$CONF" ] || printf 'tproxy-port: %s\nallow-lan: true\nmode: rule\nexternal-controller: 127.0.0.1:9090\nproxies: []\nrules:\n  - MATCH,REJECT\n' "$TPROXY_PORT" > "$CONF"
  cat > /etc/systemd/system/prism-phone-mihomo.service <<EOF
[Unit]
Description=prism-phone 网络模块（mihomo，只给云手机容器用）
After=network-online.target docker.service

[Service]
ExecStart=/usr/local/bin/mihomo -d $DATA/mihomo -f $CONF
Restart=always
RestartSec=3
# 只需要绑定端口和透明代理
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now prism-phone-mihomo
  sleep 2
  systemctl is-active --quiet prism-phone-mihomo || die "mihomo 没起来：journalctl -u prism-phone-mihomo -n 50"
  log "mihomo 已安装并运行。管家程序加参数 -mihomo-api 127.0.0.1:9090 -dns-listen $GATEWAY:$DNS_PORT 后会自动生成配置"
}

apply_rules() {
  local br
  br=$(bridge)
  systemctl is-active --quiet prism-phone-mihomo || die "mihomo 没在运行，先 install"
  if [ "${1:-}" != boot ]; then
    # 先排好自动撤销：万一规则把机器弄失联，3 分钟后自己恢复。
    systemd-run --unit=prism-phone-net-rollback --on-active=180 /bin/bash "$(readlink -f "$0")" rollback >/dev/null
    log "已安排 3 分钟后自动撤销；确认没问题后执行：bash $0 confirm"
  fi

  # 透明代理要的内核模块：内核里有，但不会自动加载（踩坑 #19）；顺便设成开机自动加载
  local m
  for m in nft_tproxy nft_socket nft_nat nft_chain_nat; do
    modprobe "$m" || die "内核没有 $m 模块，透明代理用不了"
  done
  printf '%s\n' nft_tproxy nft_socket nft_nat nft_chain_nat > /etc/modules-load.d/prism-phone.conf
  local rules
  rules=$(cat <<EOF
table ip prism_phone {
  chain pp_dns {
    type nat hook prerouting priority dstnat; policy accept;
    # 云手机的 DNS（不管它写的是哪个服务器）都交给 mihomo，不用宿主机的 DNS
    iifname "$br" ip saddr $SUBNET udp dport 53 dnat to $GATEWAY:$DNS_PORT
    iifname "$br" ip saddr $SUBNET tcp dport 53 dnat to $GATEWAY:$DNS_PORT
  }
  chain pp_tproxy {
    type filter hook prerouting priority mangle; policy accept;
    iifname != "$br" return
    ip saddr != $SUBNET return
    # 发给网关本身的（DNS、管家程序）不代理
    ip daddr $GATEWAY return
    meta l4proto tcp tproxy to 127.0.0.1:$TPROXY_PORT meta mark set $MARK accept
    meta l4proto udp tproxy to 127.0.0.1:$TPROXY_PORT meta mark set $MARK accept
  }
  chain pp_forward {
    type filter hook forward priority filter; policy accept;
    # 兜底：容器流量不允许被直接转发出去（只能经 mihomo）。透明代理的流量走 input，不经过这里。
    iifname "$br" ip saddr $SUBNET oifname != "$br" drop
  }
}
EOF
)
  # 先检查语法，有错就什么都不改（踩坑 #18：链名 tproxy 是关键字）
  printf '%s\n' "$rules" | nft -c -f - || die "规则语法不对，没有改动任何东西"
  ip rule add fwmark "$MARK" lookup "$TABLE" priority 9233 2>/dev/null || true
  ip route replace local 0.0.0.0/0 dev lo table "$TABLE"
  keep_networkd_away
  nft delete table ip prism_phone 2>/dev/null || true
  printf '%s\n' "$rules" | nft -f -
  # 甲骨文镜像自带的主机防火墙（iptables INPUT）除了 SSH 全部拒绝，透明代理和 DNS 改写后的流量算「进本机」，
  # 会被拒（踩坑 #21）。只放行：从容器网桥来、已被透明代理打标的流量，和发给 mihomo DNS 端口的流量。
  input_rules "$br" -I
  # IPv6：云手机一律不给（代理大多只有 IPv4，留着 IPv6 会绕过代理）
  if command -v ip6tables >/dev/null; then
    ip6tables -C FORWARD -i "$br" -j DROP 2>/dev/null || ip6tables -I FORWARD -i "$br" -j DROP
  fi
  log "规则已加到 $br（容器网段 $SUBNET）。宿主机自己的网络没变。"
}

# input_rules 网桥 -I（加）/ -D（删）：主机防火墙里给云手机的三条放行规则（都限定从容器网桥进来）。
input_rules() {
  local br=$1 op=$2 spec
  command -v iptables >/dev/null || return 0
  for spec in "-i $br -m mark --mark $MARK -j ACCEPT" \
              "-i $br -d $GATEWAY -p udp --dport $DNS_PORT -j ACCEPT" \
              "-i $br -d $GATEWAY -p tcp --dport $DNS_PORT -j ACCEPT"; do
    # shellcheck disable=SC2086
    if [ "$op" = -I ]; then
      iptables -C INPUT $spec 2>/dev/null || iptables -I INPUT 1 $spec
    else
      while iptables -C INPUT $spec 2>/dev/null; do iptables -D INPUT $spec; done
    fi
  done
}

# keep_networkd_away 让 systemd-networkd 不要删我们加的策略路由（踩坑 #34）。
# 它默认 ManageForeignRoutingPolicyRules=yes：每次重启（例如自动升级装了新的系统库）都会删掉「不是它加的」规则，
# 云手机就全部断网（2026-11-15 06:10 实际发生）。只写配置文件、不重启 networkd（下次它重启时生效），不会断 SSH。
keep_networkd_away() {
  [ -d /etc/systemd ] || return 0
  mkdir -p /etc/systemd/networkd.conf.d
  cat > /etc/systemd/networkd.conf.d/prism-phone.conf <<'EOC'
# prism-phone：不要清掉网络模块加的策略路由（fwmark 0x1b5 → 表 233），否则云手机全部断网（踩坑 #34）
[Network]
ManageForeignRoutingPolicyRules=no
ManageForeignRoutes=no
EOC
}

# heal 网络模块开着（prism_phone 表在）但策略路由被别的程序删了：补回去。自动更新每 10 分钟调一次。
# 网络模块没开就什么都不做。
heal() {
  nft list table ip prism_phone >/dev/null 2>&1 || return 0
  keep_networkd_away
  if ! ip rule list | grep -q "fwmark $MARK lookup $TABLE"; then
    ip rule add fwmark "$MARK" lookup "$TABLE" priority 9233
    log "策略路由被删了（云手机会断网），已补回"
  fi
  if ! ip route show table "$TABLE" | grep -q "^local default"; then
    ip route replace local 0.0.0.0/0 dev lo table "$TABLE"
    log "表 $TABLE 的本机路由被删了，已补回"
  fi
}

rollback() {
  nft delete table ip prism_phone 2>/dev/null || true
  ip rule del fwmark "$MARK" lookup "$TABLE" 2>/dev/null || true
  ip route flush table "$TABLE" 2>/dev/null || true
  local br
  br=$( (bridge) 2>/dev/null ) || true
  [ -n "$br" ] && ip6tables -D FORWARD -i "$br" -j DROP 2>/dev/null || true
  [ -n "$br" ] && input_rules "$br" -D
  systemctl stop prism-phone-net-rollback.timer 2>/dev/null || true
  systemctl disable prism-phone-net.service >/dev/null 2>&1 || true
  log "已撤销：prism_phone 表和策略路由都删了（云手机此时没有网络出口；管家程序也会禁止开机）"
}

confirm() {
  systemctl stop prism-phone-net-rollback.timer 2>/dev/null || true
  # 确认没问题后才做开机自动加规则：否则重启后规则没了，容器会经 Docker 的 NAT 直接用服务器网络。
  cat > /etc/systemd/system/prism-phone-net.service <<UNIT
[Unit]
Description=prism-phone 网络规则（云手机流量交给 mihomo）
After=docker.service prism-phone-mihomo.service prism-phone-agent.service
Requires=prism-phone-mihomo.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/bash $(readlink -f "$0") apply-boot

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  systemctl enable prism-phone-net.service >/dev/null 2>&1
  log "已确认，取消自动撤销；以后开机自动加规则"
}

status() {
  systemctl is-active prism-phone-mihomo 2>/dev/null | sed 's/^/mihomo：/'
  nft list table ip prism_phone >/dev/null 2>&1 && echo "规则：已加" || echo "规则：没加"
  systemctl list-timers --all 2>/dev/null | grep -q prism-phone-net-rollback && echo "自动撤销：等待中" || echo "自动撤销：无"
}

case "${1:-}" in
  install) install_mihomo ;;
  apply) apply_rules ;;
  apply-boot) apply_rules boot ;;
  confirm) confirm ;;
  rollback) rollback ;;
  heal) heal ;;
  status) status ;;
  *) sed -n '2,15p' "$0"; exit 1 ;;
esac
