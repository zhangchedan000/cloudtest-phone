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
  13) emu=arm64-v8a-33_r02.zip ;;   # RK3588 板子能用 Mali 的开源镜像（cnflysky lineage-20）是安卓 13
  14) emu=arm64-v8a-34_r04.zip ;;
  15) emu=arm64-v8a-35_r02.zip ;;
  *) die "不认识的安卓版本 $ver（支持 13 / 14 / 15）" ;;
esac
here=$(cd "$(dirname "$0")" && pwd)
for c in docker gcc readelf unzip losetup python3; do command -v $c >/dev/null || die "缺 $c（apt-get install -y docker.io gcc binutils unzip util-linux）"; done

W=$(mktemp -d "${PP_BUILD:-/opt/prism-phone}/tmp-wifiimg.XXXX")
V=$W/vendor_mnt; R=$W/rootfs; B=$W/build
cleanup() { grep -q " $V " /proc/mounts && umount "$V"; [ -n "${L:-}" ] && losetup -d "$L" 2>/dev/null; rm -rf "$W"; }
trap cleanup EXIT

CACHE="${PP_BUILD:-/opt/prism-phone}/cache"; mkdir -p "$CACHE"
if [ ! -s "$CACHE/$emu" ]; then
  log "下载模拟器镜像 $emu（缓存到 $CACHE，和 build-telephony 共用）"
  curl -sL --retry 3 -o "$CACHE/$emu.part" "https://dl.google.com/android/repository/sys-img/android/$emu" && mv "$CACHE/$emu.part" "$CACHE/$emu"
else
  log "用已缓存的模拟器 $CACHE/$emu"
fi
unzip -q -o "$CACHE/$emu" '*/vendor.img' -d "$W"
img=$(find "$W" -name vendor.img | head -1 || true)
L=$(losetup -fP --show -r "$img"); mkdir -p "$V"
mount -o ro "${L}p1" "$V" 2>/dev/null || mount -o ro "$L" "$V"

log "拷 WiFi 相关文件"
mkdir -p "$B/vendor/bin/hw" "$B/vendor/lib64" "$B/vendor/etc/vintf/manifest" "$B/vendor/etc/init" "$B/vendor/etc/wifi"
# WiFi HAL：安卓 14 起是 AIDL 的 android.hardware.wifi-service；安卓 13 是 HIDL 的 android.hardware.wifi@1.0-service
wifisvc=""
for f in android.hardware.wifi-service android.hardware.wifi@1.0-service; do [ -f "$V/bin/hw/$f" ] && wifisvc=$f && break; done
[ -n "$wifisvc" ] || die "模拟器 vendor 里没找到 WiFi HAL"
for f in "$wifisvc" wpa_supplicant hostapd; do cp -a "$V/bin/hw/$f" "$B/vendor/bin/hw/"; done
for f in libwifi-hal.so android.hardware.wifi-V1-ndk.so android.hardware.wifi.supplicant-V2-ndk.so android.hardware.wifi.hostapd-V1-ndk.so libkeystore-engine-wifi-hidl.so; do
  [ -f "$V/lib64/$f" ] && cp -a "$V/lib64/$f" "$B/vendor/lib64/"
done
for f in "$V"/etc/vintf/manifest/android.hardware.wifi*.xml; do cp -a "$f" "$B/vendor/etc/vintf/manifest/"; done
for f in "$V"/etc/init/android.hardware.wifi*service*.rc; do cp -a "$f" "$B/vendor/etc/init/"; done
[ -f "$V/etc/init/hostapd.android.rc" ] && cp -a "$V/etc/init/hostapd.android.rc" "$B/vendor/etc/init/"
cp -a "$V/etc/wifi/wpa_supplicant.conf" "$B/vendor/etc/wifi/"
cp "$here/wifi/wifi-supplicant.rc" "$B/vendor/etc/init/wifi-supplicant.rc"

log "拷传感器 HAL"
mkdir -p "$B/vendor/lib64/hw" "$B/vendor/etc/sensors"
# 名字各版本不一样（安卓 14 起 AIDL 的 sensors-service.multihal；更早是 HIDL 的 sensors@2.1-service.multihal），按通配拷
n=0; for f in "$V"/bin/hw/android.hardware.sensors*multihal*; do [ -f "$f" ] && cp -a "$f" "$B/vendor/bin/hw/" && n=$((n+1)); done
[ "$n" -gt 0 ] || die "模拟器 vendor 里没找到传感器 HAL"
cp -a "$V/lib64/hw/android.hardware.sensors@2.1-impl.ranchu.so" "$B/vendor/lib64/hw/"
cp -a "$V/etc/sensors/hals.conf" "$B/vendor/etc/sensors/"
for f in "$V"/etc/vintf/manifest/android.hardware.sensors*.xml; do [ -f "$f" ] && cp -a "$f" "$B/vendor/etc/vintf/manifest/"; done
for f in "$V"/etc/init/android.hardware.sensors*multihal*.rc; do [ -f "$f" ] && cp -a "$f" "$B/vendor/etc/init/"; done
python3 "$here/sensors/patch-ranchu.py" "$B/vendor/lib64/hw/android.hardware.sensors@2.1-impl.ranchu.so" \
  || log "传感器补丁没打上（这一版代码不一样）：照样构建，开机后看 dumpsys sensorservice 有没有传感器，没有就要对这一版重新分析（踩坑 #59）"

log "声明多点触控（手机里的虚拟触摸屏是 10 点触控，agent/internal/touch）"
mkdir -p "$B/vendor/etc/permissions"
cp "$here/touch/android.hardware.touchscreen.multitouch.jazzhand.xml" "$B/vendor/etc/permissions/"

log "打开电话（rild 连小助手模拟的基带，踩坑 #65）"
# Redroid 自带的 rild 只有电话接口 1.1，安卓 13 起只认 1.4 以上（「IRadio <1.4 is no longer supported」）。
# 换成模拟器的新版：libgoldfish-rild + libril-modem-lib（1.6 + AIDL 兼容层）+ 它自己的 reference-ril（支持 -d 串口）。
# 模拟器的新版 rild（接口 2.x，安卓 13 起框架只认 1.4 以上）+ 配套的 libcuttlefish-ril-2（数据结构对得上），
# 用 -p 9600 连本机 TCP，小助手在那儿扮演基带（踩坑 #65）。安卓 13、14 实测库对得上；15 的模拟器库要的 libbase 符号
# Redroid 15 没有（模拟器那份改名 libbas2.so 私用，见下面 private-libbase.py）。PP_EMU_RIL=0 可关掉（照旧用 Redroid 自带的 rild，手机像没插卡）。
emu_ril=${PP_EMU_RIL:-1}
if [ "$emu_ril" = 1 ] && [ -f "$V/bin/hw/libgoldfish-rild" ]; then
  cp -a "$V/bin/hw/libgoldfish-rild" "$B/vendor/bin/hw/"
  cp -a "$V/lib64/libril-modem-lib.so" "$V/lib64/libreference-ril.so" "$B/vendor/lib64/"
  cp -a "$V/lib64/libcuttlefish-ril-2.so" "$B/vendor/lib64/"
  [ -f "$V/lib64/hw/android.hardware.radio-library.compat.so" ] && cp -a "$V/lib64/hw/android.hardware.radio-library.compat.so" "$B/vendor/lib64/hw/"
  cp -a "$V/etc/init/rild_goldfish.rc" "$B/vendor/etc/init/"
  # 电话服务的 VINTF 声明：从模拟器的 vendor manifest.xml 里摘出 radio 相关的 <hal>
  python3 - "$V/etc/vintf/manifest.xml" "$B/vendor/etc/vintf/manifest/android.hardware.radio.prism.xml" <<'PY'
import re, sys
src = open(sys.argv[1]).read()
hals = [h for h in re.findall(r'<hal\b.*?</hal>', src, re.S) if 'android.hardware.radio' in h]
open(sys.argv[2], 'w').write('<manifest version="1.0" type="device">\n' + '\n'.join(hals) + '\n</manifest>\n')
print('电话服务声明：%d 段' % len(hals))
PY
fi
if [ "$emu_ril" = 1 ] && [ -f "$V/bin/hw/libgoldfish-rild" ]; then
  sed 's#"-d /dev/ttyC0"#"-p 9600"#' "$here/ril/init.radio.rc" > "$B/vendor/etc/init/init.radio.rc"
else
  cp "$here/ril/init.radio.rc" "$B/vendor/etc/init/init.radio.rc"
fi
cp "$here/ril/android.hardware.telephony.xml" "$B/vendor/etc/permissions/"

# 蓝牙（踩坑 #48）：安卓 14 起模拟器的蓝牙 HAL（AIDL）通过串口说 H4，串口路径看 vendor.ser.bt-uart。
# 手机里的小助手开一个伪终端放在 /dev/ttyS1，接到管家在这台手机网络里起的 rootcanal（host/build-rootcanal.sh）。
# 安卓 13 模拟器的蓝牙 HAL 走 Linux 内核蓝牙，容器里用不了（内核蓝牙不分网络命名空间）→ 不换，照旧关着。
bt=0
if [ -f "$V/bin/hw/android.hardware.bluetooth-service.default" ]; then
  log "蓝牙：换成串口蓝牙 HAL（接手机自己的 rootcanal）"
  cp -a "$V/bin/hw/android.hardware.bluetooth-service.default" "$B/vendor/bin/hw/"
  for f in "$V"/etc/init/bluetooth-service-default.rc "$V"/etc/vintf/manifest/bluetooth-service-default.xml; do
    [ -f "$f" ] && cp -a "$f" "$B/vendor/${f#$V/}"
  done
  cp "$here/bluetooth/init.bluetooth.rc" "$B/vendor/etc/init/init.bluetooth.rc"
  bt=1
fi

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
      src=""
      for d in "$V/lib64" "$V/lib64/hw"; do [ -f "$d/$l" ] && src="$d/$l" && break; done
      [ -n "$src" ] || die "$l 在模拟器 vendor 里也没有（$(basename "$f") 需要）"
      cp -a "$src" "$B/vendor/lib64/"; log "  补 $l"; added=1
    done
  done
  [ $added = 0 ] && break
done

if [ "$emu_ril" = 1 ] && [ -f "$R/vendor/lib64/libbase.so" ] && [ -f "$V/lib64/libbase.so" ]; then
  log "电话库要的 libbase 函数 Redroid 自己的有没有（安卓 15 没有 → 模拟器那份改名私用，踩坑 #65）"
  python3 "$here/ril/private-libbase.py" "$V/lib64/libbase.so" "$R/vendor/lib64/libbase.so" "$B/vendor/lib64" "$B/vendor/bin/hw"
fi

log "模拟器字样的文件名换成像真机的（踩坑 #75）"
python3 "$here/image/rename-emu.py" "$B/vendor"

log "编译时钟垫片 libppclock.so"
gcc -shared -fPIC -nostdlib -O2 -Wl,-z,max-page-size=16384 -Wl,--hash-style=both -Wl,-soname,libppclock.so \
  -o "$B/vendor/lib64/libppclock.so" "$here/wifi/ppclock.c"
chmod 644 "$B/vendor/lib64/libppclock.so"

# 去掉 Redroid 名字（踩坑 #63）：显卡驱动按机型库里的芯片名各放一份，管家开机用 androidboot.hardware=<芯片名>
HWS=$(python3 -c 'import json,glob,sys; print(" ".join(sorted({json.load(open(f)).get("values",{}).get("socModel","").lower() for f in glob.glob(sys.argv[1]+"/*.json")} - {""})))' "$here/../profiles/models")
[ -n "$HWS" ] || die "机型库里没读到芯片名"
cp "$here/image/fixup-redroid-names.sh" "$B/fixup.sh"
cat > "$B/Dockerfile" <<DF
FROM busybox:1.36.1-musl AS bb
FROM $base
COPY vendor/ /vendor/
COPY --from=bb /bin/busybox /.ppbb/busybox
COPY fixup.sh /.ppbb/fixup.sh
RUN ["/.ppbb/busybox", "sh", "-c", "/.ppbb/busybox --install -s /.ppbb && PATH=/.ppbb sh /.ppbb/fixup.sh '$HWS' && /.ppbb/busybox rm -rf /.ppbb"]
LABEL prism.wifi=1 prism.sensors=1 prism.touch=1 prism.ril=1 prism.bt=$bt prism.names=1 prism.hw="$HWS"
DF
out="${base}-wifi"
log "构建 $out"
(cd "$B" && docker build -q -t "$out" .) >/dev/null
log "完成：$out"
echo "$out"
