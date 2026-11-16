#!/usr/bin/env bash
# 编译并装上 prism-phone 的内核模块（在宿主机 / 板子上，root）：
#   pp_uts：每台手机自己的内核版本号（uname），踩坑 #64
#   pp_mnt：手机里读到的挂载表不带容器痕迹（/proc/self/mountinfo、/proc/mounts），踩坑 #68（要内核开了 KRETPROBES）
# 要当前内核的头文件（Ubuntu：apt-get install linux-headers-$(uname -r)；板子用厂商给的内核源码 / 头文件）。
# 开机自动加载（/etc/modules-load.d/prism-phone.conf）。内核升级后重跑一次。
#
# 用法：bash host/build-kmod.sh [--build-only] [模块名...]
#   不写模块名 = 全部；--build-only 只编译检查，不装不加载（不碰正在跑的系统）。
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "请用 root 运行" >&2; exit 1; }
here=$(cd "$(dirname "$0")" && pwd)
build_only=0
mods=()
for a in "$@"; do
  case "$a" in --build-only) build_only=1 ;; *) mods+=("$a") ;; esac
done
[ ${#mods[@]} -gt 0 ] || mods=(pp_uts pp_mnt)
KREL=$(uname -r)
KDIR=/lib/modules/$KREL/build
if [ ! -d "$KDIR" ]; then
  apt-get install -y "linux-headers-$KREL" >/dev/null 2>&1 || true
fi
[ -d "$KDIR" ] || { echo "没有内核头文件 $KDIR：装好头文件再跑（uname、挂载表会继续是宿主机 / 容器的样子，不影响别的）" >&2; exit 1; }
W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
ok=()
for m in "${mods[@]}"; do
  [ -d "$here/kmod/$m" ] || { echo "没有模块 $m" >&2; exit 1; }
  if [ "$m" = pp_mnt ] && ! grep -qs '^CONFIG_KRETPROBES=y' "/boot/config-$KREL" "$KDIR/.config"; then
    echo "pp_mnt：这个内核没开 KRETPROBES，跳过（挂载表照旧）" >&2; continue
  fi
  mkdir -p "$W/$m"; cp "$here/kmod/$m"/* "$W/$m/"
  rm -f "$W/$m"/test_* "$W/$m"/*.sh   # 用户态测试不进内核编译
  if ! make -s -C "$KDIR" M="$W/$m" modules; then echo "$m 编译失败" >&2; continue; fi
  echo "$m 编译通过"
  ok+=("$m")
done
[ $build_only = 1 ] && exit 0
for m in "${ok[@]}"; do
  install -D -m 644 "$W/$m/$m.ko" "/lib/modules/$KREL/extra/$m.ko"
done
depmod -a "$KREL"
# 开机自动加载：已经装过的模块都留着
conf=/etc/modules-load.d/prism-phone.conf
for m in "${ok[@]}"; do grep -qx "$m" "$conf" 2>/dev/null || echo "$m" >> "$conf"; done
for m in "${ok[@]}"; do
  modprobe -r "$m" 2>/dev/null || true
  modprobe "$m" && [ -e "/proc/$m" ] && echo "$m 已装上（/proc/$m）"
done
