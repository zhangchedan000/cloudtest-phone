#!/usr/bin/env bash
# 一条命令，从开源基础镜像叠出板子的「安卓13 完整正式镜像」：
#   SIM/电话 + Mali 显卡 + 拨号短信 + Google Play + 干净指纹，全在里面。
#
# 它就是把下面 4 步串起来（每步见各自脚本注释，踩坑 #99/#103/#104）：
#   cnflysky/redroid-rk3588:lineage-20            ← 开源基础镜像（安卓13 带 Mali，docker pull）
#     → build-wifi-image.sh        → -wifi         （加 WiFi/传感器/电话 HAL）
#     → build-telephony-image.sh   → -wifi-tel     （补 AOSP 电话应用）
#     → build-resign-image.sh      → -wifi-tel-resign      （全平台 testkey 重签，过签名墙，排除 Google）
#     → build-apps-image.sh        → -wifi-tel-resign-apps （加拨号/短信）★最终镜像
#
# 平时换板【不用】跑这个——直接 docker load 老板子存的 tarball 更快（几分钟）。
# 只有「丢了 tarball」或「要改镜像」时才从头重建（约 20~30 分钟：下 ~800MB 模拟器镜像 + 重签 154 个 apk + 构建）。
#
# 前提工具（板子 Alpine）：apk add openjdk17-jre-headless zip ；gcc/readelf/unzip/losetup/python3 一般已有。
# 大文件目录：PP_TOOLS/PP_CACHE/PP_BUILD 默认 /mmc/pp-build（板子 eMMC 根分区太小，别用默认 /opt）。
#
# 用法（root，在板子上）：
#   PP_TOOLS=/mmc/pp-build/tools PP_CACHE=/mmc/pp-build/cache PP_BUILD=/mmc/pp-build \
#     bash host/build-board-image.sh
#   跑完会 docker tag 出最终镜像；想存成 tarball：
#   docker save <最终镜像> | gzip -1 > /mmc/pp-dist/android13-full.tar.gz
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
log(){ printf '\n\033[1m[board-image] ===== %s =====\033[0m\n' "$*"; }

BASE=${1:-cnflysky/redroid-rk3588:lineage-20}
VER=${2:-13}   # 安卓大版本：13=cnflysky lineage；14=板子的 Mali 底子(prism-redroid-mali:14 或 :14-gms 带谷歌)
# 镜像名是确定的（各脚本固定加后缀），直接推算，省得解析输出
WIFI="${BASE}-wifi"
TEL="${WIFI}-tel"
RESIGN="${TEL}-resign"
APPS="${RESIGN}-apps"

log "0/5 拉开源基础镜像 $BASE"
docker image inspect "$BASE" >/dev/null 2>&1 || docker pull "$BASE"

log "1/5 WiFi/传感器/电话HAL → $WIFI"
bash "$here/build-wifi-image.sh" "$BASE" "$VER"

log "2/5 补电话应用 → $TEL"
bash "$here/build-telephony-image.sh" "$WIFI" "$VER"

log "3/5 全平台 testkey 重签 → $RESIGN"
bash "$here/build-resign-image.sh" "$TEL"

log "4/5 加拨号/短信 → $APPS"
bash "$here/build-apps-image.sh" "$RESIGN" "$VER"

log "5/5 校验最终镜像在不在"
docker image inspect "$APPS" >/dev/null 2>&1 || { echo "最终镜像没生成，往上翻哪步报错"; exit 1; }
printf '\n\033[1m完成！最终镜像：%s\033[0m\n' "$APPS"
echo "（存 tarball：docker save '$APPS' | gzip -1 > /mmc/pp-dist/android${VER}-full.tar.gz）"
echo "$APPS"
