#!/bin/bash
# 从小米官方「完整包」（recovery ROM，*-ota_full-*.zip）取各分区 build.prop，给 host/extract-buildprops.py 用（踩坑 #52）。
# 网上没有现成固件转储的机型 / 版本用这个。只读取、不刷机；取完删掉所有临时文件（约 15GB 临时空间）。
#
# 用法（root，在 ARM64 宿主机上）：
#   bash host/ota-buildprops.sh <完整包 URL> <输出目录>
#   python3 host/extract-buildprops.py <输出目录> --sku <机型代号> --model profiles/models/x.json --android 15
#
# 下载地址：完整包文件名在 xmfirmwareupdater.com 之类的更新站能查到（带一段校验码，如
#   aristotle_global-ota_full-OS2.0.214.0.VMFMIXM-user-15.0-82b52f532f.zip）。
#   官方 bigota.d.miui.com 从阿什本只有几十 KB/s，换镜像 bn.d.miui.com（实测 9MB/s）：https://bn.d.miui.com/<版本>/<文件名>
# 注意：小米的分区是 erofs（LZ4），先试 mount -t erofs，挂不上用 fsck.erofs --extract；
#   erofs-utils 1.7 的 dump.erofs 没有 --cat，不能直接读单个文件（踩过）。
set -euo pipefail
URL=$1; OUT=$2
[ "$(id -u)" = 0 ] || { echo "请用 root 运行"; exit 1; }
command -v fsck.erofs >/dev/null || DEBIAN_FRONTEND=noninteractive apt-get install -y -qq erofs-utils
W=$(mktemp -d /opt/prism-phone/tmp-ota.XXXX)
trap 'for m in "$W"/mnt/*; do grep -q " $m " /proc/mounts && umount "$m"; done; rm -rf "$W"' EXIT
cd "$W"
curl -fsSL -o pd.tgz https://github.com/ssut/payload-dumper-go/releases/download/1.3.0/payload-dumper-go_1.3.0_linux_arm64.tar.gz
tar xzf pd.tgz
echo "[ota] 下载 $URL"
curl -fsSL --retry 3 -o ota.zip "$URL"
unzip -o -q ota.zip payload.bin && rm -f ota.zip
parts=system,system_ext,product,vendor,odm,mi_ext,vendor_dlkm,odm_dlkm,system_dlkm
./payload-dumper-go -c 4 -p "$parts" -o imgs payload.bin >/dev/null
rm -f payload.bin
modprobe erofs || true
rm -rf "$OUT"; mkdir -p "$OUT"
for p in ${parts//,/ }; do
  img=imgs/$p.img; [ -f "$img" ] || continue
  src=""
  mkdir -p "mnt/$p"
  if mount -t erofs -o loop,ro "$img" "mnt/$p" 2>/dev/null; then src=mnt/$p
  else mkdir -p "ext/$p" && fsck.erofs --extract="ext/$p" "$img" >/dev/null 2>&1 && src=ext/$p; fi
  [ -n "$src" ] || { echo "[ota] 读不了 $p"; continue; }
  for f in $(cd "$src" && find . -maxdepth 3 -type f \( -name "build.prop" -o -name "*_build.prop" -o -name "build_*.prop" \)); do
    cp "$src/$f" "$OUT/$(echo "$p/${f#./}" | tr '/' '.')"
  done
  grep -q " $W/mnt/$p " /proc/mounts && umount "mnt/$p"
  rm -rf "ext/$p" "$img"
done
echo "[ota] 取到：$(ls "$OUT" | tr '\n' ' ')"
grep -h "ro.sf.lcd_density" "$OUT"/* 2>/dev/null | sort -u || true
