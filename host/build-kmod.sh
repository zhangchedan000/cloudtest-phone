#!/usr/bin/env bash
# 编译并装上 prism-phone 的内核模块（在宿主机 / 板子上，root）：
#   pp_uts：每台手机自己的内核版本号（uname），踩坑 #64
# 要当前内核的头文件（Ubuntu：apt-get install linux-headers-$(uname -r)；板子用厂商给的内核源码 / 头文件）。
# 开机自动加载（/etc/modules-load.d/prism-phone.conf）。内核升级后重跑一次。
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "请用 root 运行" >&2; exit 1; }
here=$(cd "$(dirname "$0")" && pwd)
KREL=$(uname -r)
KDIR=/lib/modules/$KREL/build
if [ ! -d "$KDIR" ]; then
  apt-get install -y "linux-headers-$KREL" >/dev/null 2>&1 || true
fi
[ -d "$KDIR" ] || { echo "没有内核头文件 $KDIR：装好头文件再跑（uname 会继续显示宿主机内核，不影响别的）" >&2; exit 1; }
W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
cp "$here/kmod/pp_uts/pp_uts.c" "$here/kmod/pp_uts/Makefile" "$W/"
make -s -C "$KDIR" M="$W" modules
install -D -m 644 "$W/pp_uts.ko" "/lib/modules/$KREL/extra/pp_uts.ko"
depmod -a "$KREL"
echo pp_uts > /etc/modules-load.d/prism-phone.conf
modprobe -r pp_uts 2>/dev/null || true
modprobe pp_uts
[ -e /proc/pp_uts ] && echo "pp_uts 已装上（/proc/pp_uts）"
