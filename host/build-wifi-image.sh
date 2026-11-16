#!/usr/bin/env bash
# 给 Redroid 镜像加 WiFi（踩坑 #57）和传感器（踩坑 #59）：从谷歌官方安卓模拟器镜像（同版本、arm64、纯 AOSP）的 vendor 分区
# 拷 WiFi HAL、wpa_supplicant、hostapd、传感器 HAL 和它们在 vendor 命名空间里缺的库，再加一个时钟垫片，产出 <原镜像>-wifi。
# 传感器 HAL 插件打一处补丁（host/sensors/patch-ranchu.py），数据由管家的 pp-init 在手机里喂（internal/sensors）。
# 宿主机那边（mac80211_hwsim 虚拟网卡、每台手机的「路由器」netns、热点）由管家程序开机时配（agent wifi.go）。
#
# 用法（root，ARM64 宿主机，装好 docker）：
#   bash host/build-wifi-image.sh redroid/redroid:14.0.0_mindthegapps        # → redroid/redroid:14.0.0_mindthegapps-wifi
#   bash host/build-wifi-image.sh redroid/redroid:14.0.0_64only-latest 14
#   第二个参数是安卓版本（默认从镜像名猜）；模拟器镜像按版本选（14 → android-34，15 → android-35）。
#
# 产出镜像带标签 prism.wifi=1、prism.sensors=1，管家程序据此决定给这台手机配 WiFi（没有这个标签就照旧用有线 eth0）。
set -euo pipefail
log() { printf '[wifi] %s\n' "$*"; }
die() { printf '[wifi] 错误：%s\n' "$*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die "请用 root 运行"
base=${1:?用法：build-wifi-image.sh <redroid 镜像> [安卓版本]}
ver=${2:-$(echo "$base" | grep -oE ':[0-9]+' | tr -d ':')}
case "$ver" in
  14) emu=arm64-v8a-34_r04.zip ;;
  15) emu=arm64-v8a-35_r02.zip ;;
  *) die "不认识的安卓版本 $ver（支持 14 / 15）" ;;
esac
here=$(cd "$(dirname "$0")" && pwd)
for c in docker gcc readelf unzip losetup python3; do command -v $c >/dev/null || die "缺 $c（apt-get install -y docker.io gcc binutils unzip util-linux）"; done

W=$(mktemp -d /opt/prism-phone/tmp-wifiimg.XXXX)
V=$W/vendor_mnt; R=$W/rootfs; B=$W/build
cleanup() { grep -q " $V " /proc/mounts && umount "$V"; [ -n "${L:-}" ] && losetup -d "$L" 2>/dev/null; rm -rf "$W"; }
trap cleanup EXIT

log "下载模拟器镜像 $emu（谷歌官方 sys-img/android）"
curl -sL --retry 3 -o "$W/emu.zip" "https://dl.google.com/android/repository/sys-img/android/$emu"
unzip -q -o "$W/emu.zip" '*/vendor.img' -d "$W"
img=$(find "$W" -name vendor.img | head -1)
L=$(losetup -fP --show -r "$img"); mkdir -p "$V"
mount -o ro "${L}p1" "$V" 2>/dev/null || mount -o ro "$L" "$V"

log "拷 WiFi 相关文件"
mkdir -p "$B/vendor/bin/hw" "$B/vendor/lib64" "$B/vendor/etc/vintf/manifest" "$B/vendor/etc/init" "$B/vendor/etc/wifi"
for f in android.hardware.wifi-service wpa_supplicant hostapd; do cp -a "$V/bin/hw/$f" "$B/vendor/bin/hw/"; done
for f in libwifi-hal.so android.hardware.wifi-V1-ndk.so android.hardware.wifi.supplicant-V2-ndk.so android.hardware.wifi.hostapd-V1-ndk.so libkeystore-engine-wifi-hidl.so; do
  [ -f "$V/lib64/$f" ] && cp -a "$V/lib64/$f" "$B/vendor/lib64/"
done
for f in "$V"/etc/vintf/manifest/android.hardware.wifi*.xml; do cp -a "$f" "$B/vendor/etc/vintf/manifest/"; done
cp -a "$V/etc/init/android.hardware.wifi-service.rc" "$B/vendor/etc/init/"
[ -f "$V/etc/init/hostapd.android.rc" ] && cp -a "$V/etc/init/hostapd.android.rc" "$B/vendor/etc/init/"
cp -a "$V/etc/wifi/wpa_supplicant.conf" "$B/vendor/etc/wifi/"
cp "$here/wifi/wifi-supplicant.rc" "$B/vendor/etc/init/wifi-supplicant.rc"

log "拷传感器 HAL"
mkdir -p "$B/vendor/lib64/hw" "$B/vendor/etc/sensors"
cp -a "$V/bin/hw/android.hardware.sensors-service.multihal" "$B/vendor/bin/hw/"
cp -a "$V/lib64/hw/android.hardware.sensors@2.1-impl.ranchu.so" "$B/vendor/lib64/hw/"
cp -a "$V/etc/sensors/hals.conf" "$B/vendor/etc/sensors/"
cp -a "$V/etc/vintf/manifest/android.hardware.sensors-multihal.xml" "$B/vendor/etc/vintf/manifest/"
cp -a "$V/etc/init/android.hardware.sensors-service-multihal.rc" "$B/vendor/etc/init/"
python3 "$here/sensors/patch-ranchu.py" "$B/vendor/lib64/hw/android.hardware.sensors@2.1-impl.ranchu.so"

log "声明多点触控（手机里的虚拟触摸屏是 10 点触控，agent/internal/touch）"
mkdir -p "$B/vendor/etc/permissions"
cp "$here/touch/android.hardware.touchscreen.multitouch.jazzhand.xml" "$B/vendor/etc/permissions/"

log "补齐 vendor 命名空间里缺的库（递归）"
id=$(docker create "$base"); mkdir -p "$R"
docker export "$id" | tar -x -C "$R" --wildcards 'vendor/lib64/*' 'system/apex/com.android.vndk.current/*' 2>/dev/null || true
docker rm "$id" >/dev/null
LLNDK=" libc.so libm.so libdl.so liblog.so libbinder_ndk.so libz.so libsync.so libvndksupport.so libnativewindow.so libandroid_net.so libmediandk.so libvulkan.so libEGL.so libGLESv2.so libGLESv1_CM.so libRS.so libft2.so libselinux.so libcgrouprc.so "
have() { [ -f "$B/vendor/lib64/$1" ] || [ -f "$R/vendor/lib64/$1" ] || [ -f "$R/system/apex/com.android.vndk.current/lib64/$1" ] || [[ "$LLNDK" == *" $1 "* ]]; }
for round in 1 2 3 4 5 6 7 8; do
  added=0
  for f in "$B"/vendor/bin/hw/* "$B"/vendor/lib64/*.so "$B"/vendor/lib64/hw/*.so; do
    for l in $(readelf -d "$f" 2>/dev/null | sed -n 's/.*NEEDED.*\[\(.*\)\]/\1/p'); do
      have "$l" && continue
      [ -f "$V/lib64/$l" ] || die "$l 在模拟器 vendor 里也没有（$(basename "$f") 需要）"
      cp -a "$V/lib64/$l" "$B/vendor/lib64/"; log "  补 $l"; added=1
    done
  done
  [ $added = 0 ] && break
done

log "编译时钟垫片 libppclock.so"
gcc -shared -fPIC -nostdlib -O2 -Wl,-z,max-page-size=16384 -Wl,--hash-style=both -Wl,-soname,libppclock.so \
  -o "$B/vendor/lib64/libppclock.so" "$here/wifi/ppclock.c"
chmod 644 "$B/vendor/lib64/libppclock.so"

cat > "$B/Dockerfile" <<DF
FROM $base
COPY vendor/ /vendor/
LABEL prism.wifi=1 prism.sensors=1 prism.touch=1
DF
out="${base}-wifi"
log "构建 $out"
(cd "$B" && docker build -q -t "$out" .) >/dev/null
log "完成：$out"
echo "$out"
