#!/usr/bin/env bash
# 构建「带谷歌服务（GMS / Play 商店）」的 Redroid 镜像，给需要谷歌服务的云手机用。
#
# 原理：用 ayasa520/redroid-script 往官方 redroid 镜像里打包 MindTheGapps，产出
#       redroid/redroid:<版本>_mindthegapps。管家程序用 -gapps-images 指到这个镜像，
#       新建手机时勾「带谷歌服务」就用它开机（见 docs 和 service.go imageFor）。
#
# 用法（root，在 ARM64 机器上，装好 docker）：
#   bash host/build-gapps-image.sh            # 默认安卓 14
#   bash host/build-gapps-image.sh 14         # 指定安卓大版本（redroid-script 目前最高 14）
#   bash host/build-gapps-image.sh 14 --force # 已存在也重建
#
# 产出镜像名会打印在最后，并给出要加到 /etc/prism-phone/agent.env 的 -gapps-images 片段。
# 注意：镜像较大（约 4GB），构建要下载 GApps 包，需要能访问 GitHub / 谷歌。
set -euo pipefail

log() { printf '[gapps] %s\n' "$*"; }
die() { printf '[gapps] 错误：%s\n' "$*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die "请用 root 运行"
command -v docker >/dev/null || die "没有 docker（先运行 host/setup-host.sh）"
command -v git >/dev/null || die "没有 git"
command -v python3 >/dev/null || die "没有 python3"

ver=${1:-14}
force=0
[ "${2:-}" = "--force" ] && force=1
case "$ver" in 15) full=15.0.0 ;; 14) full=14.0.0 ;; 13) full=13.0.0 ;; 12) full=12.0.0 ;; *) die "不支持的安卓版本 $ver（支持 12/13/14/15）" ;; esac

image="redroid/redroid:${full}_mindthegapps"
if [ "$force" = 0 ] && docker image inspect "$image" >/dev/null 2>&1; then
  log "镜像已存在：$image（要重建加 --force）"
  echo "-gapps-images ${ver}=${image}"
  exit 0
fi

# redroid-script 需要的系统工具（跨平台：板子 Alpine 用 apk，阿什本 Debian 用 apt）
log "装构建依赖（lzip unzip）"
if command -v apk >/dev/null; then
  apk add --no-cache lzip unzip >/dev/null 2>&1 || die "装依赖失败（apk add lzip unzip）"
elif command -v apt-get >/dev/null; then
  apt-get update -qq && apt-get install -y -qq lzip unzip >/dev/null || die "装依赖失败（apt）"
fi

# 大文件目录：板子 eMMC 根分区太小，用 PP_BUILD（默认 /mmc/pp-build）
work="${PP_BUILD:-/opt/prism-phone}/redroid-script"
if [ ! -d "$work/.git" ]; then
  log "克隆 redroid-script 到 $work"
  rm -rf "$work"
  git clone --depth 1 https://github.com/ayasa520/redroid-script "$work" || die "克隆 redroid-script 失败"
else
  log "更新 redroid-script"
  git -C "$work" pull --ff-only || true
fi

if [ -f "$work/requirements.txt" ]; then
  log "装 python 依赖"
  python3 -m pip install -q -r "$work/requirements.txt" 2>/dev/null || python3 -m pip install -q --break-system-packages -r "$work/requirements.txt" || true
fi

# redroid.py 的 -a 选项把安卓 15 挡掉了（choices 没列），但底层 MindTheGapps 支持 15（stuff/mindthegapps.py 有 15.0.0 的 arm64 包）。
# 把 $full 加进 choices 再跑（安卓 15 的整版底座 redroid/redroid:15.0.0-latest dockerhub 上有）。
grep -q "'$full'" "$work/redroid.py" || sed -i "s/choices=\['14.0.0'/choices=['$full', '14.0.0'/" "$work/redroid.py"

log "构建 $image（安卓 $full + MindTheGapps），这步较慢……"
( cd "$work" && python3 redroid.py -a "$full" -mtg -c docker ) || die "构建失败（看上面的日志；常见原因：下载 GApps 包的网络不通）"

docker image inspect "$image" >/dev/null 2>&1 || die "构建跑完了但没找到镜像 $image，去 docker images 看实际产出的标签"

log "完成：$image"
log "把下面这段加到 /etc/prism-phone/agent.env 的 AGENT_ARGS 后面，然后 systemctl restart prism-phone-agent："
echo "    -gapps-images ${ver}=${image}"
