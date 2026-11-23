#!/bin/sh
# 按板子正在跑的内核（魔云腾 Q1：6.1.141，Rockchip SDK 内核）编内核模块，先只编不装（踩坑 #129）。
# 魔云腾没给内核源码 / 头文件：用 Rockchip 官方 develop-6.1 里合入 v6.1.141 的那个提交 + 板子的 /proc/config.gz，
# 在 Debian 容器里原生编（数据都在 /mmc，不碰根分区），然后拿板子内核里抽出来的 CRC 逐个核对。
# 用法（root，板子上）：sh host/kmod/board/build.sh [模块 ...]（默认 mac80211_hwsim）
set -eu
here=$(cd "$(dirname "$0")" && pwd)
K=${PP_KMOD:-/mmc/pp-build/kmod}
SHA=${PP_KSRC_SHA:-cb2c6fee8a36d85b5ead8f246bbc035259b14b2a}   # rockchip-linux/kernel develop-6.1 "Merge tag 'v6.1.141'"
mkdir -p "$K"
log(){ echo "$(date +%T) $*"; }
# 1) 板子内核的 CRC 表 + 配置
[ -s "$K/boot.img" ] || cat /dev/mmcblk0p5 > "$K/boot.img"      # 只读拷 boot 分区
python3 "$here/extract-symvers.py" "$K/boot.img" > "$K/Module.symvers.board"
zcat /proc/config.gz > "$K/config.board"
log "板子内核导出符号 $(wc -l < "$K/Module.symvers.board") 个"
# 2) 源码（只拉这一个提交）
if [ ! -f "$K/src/Makefile" ]; then
  rm -rf "$K/src"; git init -q "$K/src"
  (cd "$K/src" && git remote add origin https://github.com/rockchip-linux/kernel && git fetch -q --depth 1 origin "$SHA" && git checkout -q FETCH_HEAD)
fi
log "源码 $(sed -n 's/^SUBLEVEL = //p' "$K/src/Makefile" | head -1) @ $(cd "$K/src" && git rev-parse --short HEAD)"
cp "$here/compare-crcs.py" "$K/"
# 3) 在 Debian 容器里编：配置 = 板子的 + 要的模块；vmlinux 编完 modpost 出我们自己的 CRC，再编模块
rm -f "$K/mac80211_hwsim.ko" "$K/src/drivers/net/wireless/mac80211_hwsim.ko"
docker run --rm --platform linux/arm64 -v "$K:/k" -w /k/src debian:12-slim bash -ec '
  set -o pipefail
  # 这个内核上 overlay 报的剩余空间不对（容器里 df 显示满），apt 下载前查空间会拒绝 → 下载缓存放挂进来的 /k（真 ext4）
  mkdir -p /k/aptcache/partial
  apt-get update -qq && apt-get install -y -qq --no-install-recommends -o Dir::Cache::Archives=/k/aptcache build-essential bc flex bison libssl-dev libelf-dev kmod python3 cpio >/dev/null
  cp /k/config.board .config
  scripts/config --module MAC80211_HWSIM
  scripts/config --set-str INITRAMFS_SOURCE ""   # 魔云腾的配置把它构建机上的 initrd.cpio.gz 编进内核，我们没有；和模块 / CRC 无关
  touch .scmversion                         # 版本号不带 "+"（要和板子的 6.1.141 一字不差）
  make -s olddefconfig
  echo "vermagic: $(make -s kernelrelease)"
  make -s -j"$(nproc)" vmlinux modules_prepare > /k/make-vmlinux.log 2>&1 || { tail -20 /k/make-vmlinux.log; exit 1; }
  # 6.1 只编 vmlinux 时出的是 vmlinux.symvers，Module.symvers 要编模块时 modpost 才生成；后面编外部模块（核对 CRC 的
  # crctest、我们的模块）要用它，先照 vmlinux.symvers 放一份（以前目录里一直有上次留下的，从空目录编才暴露，2026-11-23）
  test -s vmlinux.symvers || { echo "没有 vmlinux.symvers（vmlinux 没编完）"; exit 1; }
  test -s Module.symvers || cp vmlinux.symvers Module.symvers
  make -s -j"$(nproc)" drivers/net/wireless/mac80211_hwsim.ko 2>&1 | tail -5
  modinfo -F vermagic drivers/net/wireless/mac80211_hwsim.ko
'
grep -q '"module_layout"\|__versions' "$K/src/drivers/net/wireless/mac80211_hwsim.mod.c" || { log "✗ 模块里没有符号版本表（vmlinux 没编好），不能用"; exit 1; }
cp "$K/src/drivers/net/wireless/mac80211_hwsim.ko" "$K/"
log "编好：$K/mac80211_hwsim.ko；核对 CRC："
python3 "$K/compare-crcs.py" "$K/src/drivers/net/wireless/mac80211_hwsim.mod.c" "$K/Module.symvers.board"
