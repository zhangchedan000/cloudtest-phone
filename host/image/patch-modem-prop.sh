#!/bin/sh
# 给【已经做好】的安卓 16 镜像补上「电话 HAL 属性名去 qemu 字样」（踩坑 #127），不用整条链重建：
#   /vendor/bin/hw/android.hardware.radio-service.mtk 里 vendor.qemu.vport.modem → vendor.rild.vport.modem（等长）
#   /vendor/etc/init/init.radio.rc 里 setprop 跟着改
# 新做的镜像不用跑这个（build-wifi-image.sh → rename-emu.py 已经改了）。
# 原镜像留成 <镜像>-orig（确认没问题后 docker rmi 掉）。重复跑没关系（已经改过的跳过）。
# 用法（root，板子上）：sh host/image/patch-modem-prop.sh <镜像> [<镜像> ...]
set -eu
OLD=vendor.qemu.vport.modem
NEW=vendor.rild.vport.modem
HAL=/vendor/bin/hw/android.hardware.radio-service.mtk
RC=/vendor/etc/init/init.radio.rc
mkdir -p "${PP_BUILD:-/mmc/pp-build}"
for img in "$@"; do
  W=$(mktemp -d "${PP_BUILD:-/mmc/pp-build}/modemprop.XXXX")
  cid=$(docker create "$img")
  if ! docker cp "$cid:$HAL" "$W/hal" 2>/dev/null || ! docker cp "$cid:$RC" "$W/init.radio.rc" 2>/dev/null; then
    docker rm "$cid" >/dev/null; rm -rf "$W"; echo "· $img：没有 ranchu 电话 HAL（不是安卓 16 电话镜像），跳过"; continue
  fi
  docker rm "$cid" >/dev/null
  if ! grep -q "$OLD" "$W/hal" && ! grep -q "$OLD" "$W/init.radio.rc"; then
    rm -rf "$W"; echo "✓ $img：已经改过了"; continue
  fi
  python3 - "$W/hal" "$OLD" "$NEW" <<'EOF'
import sys
p, old, new = sys.argv[1], sys.argv[2].encode(), sys.argv[3].encode()
assert len(old) == len(new)
b = open(p, "rb").read()
n = b.count(old)
assert n == 1, f"HAL 里 {old!r} 出现 {n} 次（应为 1）"
open(p, "wb").write(b.replace(old, new))
EOF
  sed -i "s/$OLD/$NEW/" "$W/init.radio.rc"
  grep -q "$NEW" "$W/init.radio.rc" || { echo "✗ $img：init.radio.rc 里没找到 setprop"; exit 1; }
  chmod 755 "$W/hal"; chmod 644 "$W/init.radio.rc"
  printf 'FROM %s\nCOPY hal %s\nCOPY init.radio.rc %s\n' "$img" "$HAL" "$RC" > "$W/Dockerfile"
  docker image inspect "$img-orig" >/dev/null 2>&1 || docker tag "$img" "$img-orig"
  docker build -q -t "$img" "$W" >/dev/null
  rm -rf "$W"
  echo "✓ $img：已改（原镜像留在 $img-orig）"
done
