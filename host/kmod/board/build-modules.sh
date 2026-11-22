#!/bin/sh
# 用【板子内核自带的头文件】编我们自己的内核模块（踩坑 #129 / #134）。先跑过 build.sh、build-kheaders.sh
# （编译工具链 + 已换成板子头文件的源码树 $K/src）。
#
# 编三个：
#   pp_mnt.ko  手机里读到的挂载表 / 输入设备 / CPU 时间 / 开机时间 / 本机端口 / 模块列表 / 网卡名去痕迹（host/kmod/pp_mnt）
#   pp_uts.ko  每台手机自己的内核版本号 uname（host/kmod/pp_uts）
#   wlan.ko    虚拟 WiFi 驱动：内核的 mac80211_hwsim 改个名——模块名、驱动名、/sys/class 里的类名、设备名都不带 hwsim
#              （手机里 /sys/class/net/wlan0 指向 /sys/devices/virtual/<类名>/<设备名>/…，原来一看就是 WiFi 模拟器）。
#              管家用的 netlink 接口（MAC80211_HWSIM）不变。
# 产物在 $K/out/，用 host/kmod/board/install-modules.sh 装到仓库 tools/dist/kmod/<板子>/。
#
# 用法（root，板子上，仓库目录里）：sh host/kmod/board/build-modules.sh
set -eu
K=${PP_KMOD:-/mmc/pp-build/kmod}
here=$(cd "$(dirname "$0")/.." && pwd)   # host/kmod
log(){ echo "$(date +%T) $*"; }
[ -d "$K/src/include.public" ] && grep -q "CONFIG_MYT_FS 1" "$K/src/include/generated/autoconf.h" || {
  echo "源码树还没换成板子头文件：先跑 build.sh、build-kheaders.sh"; exit 1; }
[ -s "$K/Module.symvers.board" ] || { echo "缺 $K/Module.symvers.board（build.sh 生成）"; exit 1; }

rm -rf "$K/ext" "$K/out"; mkdir -p "$K/ext" "$K/out"
for m in pp_mnt pp_uts; do
  mkdir -p "$K/ext/$m"
  cp "$here/$m"/*.c "$K/ext/$m/"
  for h in "$here/$m"/*.h; do [ -f "$h" ] && cp "$h" "$K/ext/$m/"; done
  rm -f "$K/ext/$m"/test_*
  echo "obj-m := $m.o" > "$K/ext/$m/Kbuild"
done

# WiFi 驱动改名：文件名决定模块名（KBUILD_MODNAME=wlan）
W=$K/ext/wlan; mkdir -p "$W"
cp "$K/src/drivers/net/wireless/mac80211_hwsim.c" "$W/wlan.c"
cp "$K/src/drivers/net/wireless/mac80211_hwsim.h" "$W/"
sed -i \
  -e 's/class_create(THIS_MODULE, "mac80211_hwsim")/class_create(THIS_MODULE, "wlan")/' \
  -e 's/"hwsim%d", idx)/"wlan%d", idx)/' \
  -e '/static struct platform_driver mac80211_hwsim_driver = {/,/^};/s/\.name = "mac80211_hwsim"/.name = "wlan"/' \
  "$W/wlan.c"
for want in 'class_create(THIS_MODULE, "wlan")' '"wlan%d", idx)' '.name = "wlan"' '.name = "MAC80211_HWSIM"'; do
  grep -qF "$want" "$W/wlan.c" || { echo "改名没改上：$want（内核源码变了？）"; exit 1; }
done
echo 'obj-m := wlan.o' > "$W/Kbuild"

docker run --rm --platform linux/arm64 -v "$K:/k" -w /k/src debian:12-slim bash -ec '
  set -o pipefail
  apt-get update -qq && apt-get install -y -qq --no-install-recommends -o Dir::Cache::Archives=/k/aptcache build-essential bc flex bison libssl-dev libelf-dev kmod python3 >/dev/null
  cp /k/Module.symvers.board Module.symvers
  grep -q "CONFIG_MYT_FS 1" include/generated/autoconf.h
  for m in pp_mnt pp_uts wlan; do
    echo "== $m"
    make -s M=/k/ext/$m modules 2>&1 | tail -20
    cp /k/ext/$m/$m.ko /k/out/
    modinfo -F vermagic /k/out/$m.ko
  done
'
for m in pp_mnt pp_uts wlan; do
  log "$m：$(python3 "$K/compare-crcs.py" "$K/ext/$m/$m.mod.c" "$K/Module.symvers.board" | head -1)"
  python3 "$K/compare-crcs.py" "$K/ext/$m/$m.mod.c" "$K/Module.symvers.board" >/dev/null || { echo "$m 引用的内核符号板子上没有 / 对不上"; exit 1; }
done
( cd "$K/out" && sha256sum *.ko )
log "✓ 编好：$K/out/{pp_mnt,pp_uts,wlan}.ko"
