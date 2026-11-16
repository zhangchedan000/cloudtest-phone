#!/usr/bin/env bash
# 编译 hostapd 2.11 装到 /usr/local/sbin/hostapd（踩坑 #57）：
# Ubuntu 自带的 hostapd 2.10 在新内核（6.17）上配 WPA2 热点会报 "key addition failed"，2.11 正常。
# 已经是 2.11 及以上就跳过。用法（root）：bash host/build-hostapd.sh
set -euo pipefail
log() { printf '[hostapd] %s\n' "$*"; }
[ "$(id -u)" = 0 ] || { echo "请用 root 运行" >&2; exit 1; }
if /usr/local/sbin/hostapd -v 2>&1 | grep -qE 'v2\.(1[1-9]|[2-9][0-9])'; then log "已装：$(/usr/local/sbin/hostapd -v 2>&1 | head -1)"; exit 0; fi
apt-get install -y -qq build-essential pkg-config libnl-3-dev libnl-genl-3-dev libssl-dev >/dev/null
W=$(mktemp -d /opt/prism-phone/tmp-hapd.XXXX 2>/dev/null || mktemp -d); trap 'rm -rf "$W"' EXIT
curl -sL --retry 3 -o "$W/h.tgz" https://w1.fi/releases/hostapd-2.11.tar.gz
tar -xzf "$W/h.tgz" -C "$W"
cd "$W"/hostapd-2.11/hostapd
cat > .config <<CFG
CONFIG_DRIVER_NL80211=y
CONFIG_LIBNL32=y
CONFIG_IEEE80211N=y
CONFIG_IEEE80211AC=y
CONFIG_IEEE80211W=y
CONFIG_TLS=openssl
CONFIG_CTRL_IFACE=y
CFG
make -s -j"$(nproc)" >/dev/null
install -m 755 hostapd /usr/local/sbin/hostapd
log "装好：$(/usr/local/sbin/hostapd -v 2>&1 | head -1)"
