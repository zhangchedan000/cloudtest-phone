#!/usr/bin/env bash
# 安卓15 专用：把魔云腾 dobox Q15 的整套 /system/framework（framework.jar / services.jar +
# 它们 AOT 进 boot image 的 boot-*.{art,oat,vdex} + /system/framework/oat 的 odex）整体 graft
# 进基础镜像，产出 <base>-fw（标签 prism.framework=dobox）。
#
# 为什么（踩坑 #118）：A15 真代理启动时 system_server 反复崩 getRequiredPermissionControllerLPr
# （"exactly one permissions manager; found []"，PMS 把 apex 承载的 PermissionController 判成
# "no longer exists" 擦掉）→ 崩溃死循环，开不了机。查明 dobox Q15 和我们 redroid15 是同一套 AOSP15
# 构建（telephony-common.jar md5 相同），但 dobox 改了 services.jar（含 PackageManagerService）和
# framework.jar 来适配容器、避开这个崩溃。apex 形态、/data 形态都实测排除了，真因就是这两个框架 jar。
# dobox 是实证能用的商业产品 → 照搬它的整套框架（含匹配的 boot image AOT，像搬 Mali 一样搬匹配集）。
#
# 关键：framework.jar/services.jar 被 AOT 进 boot image，只换 .jar 不生效——所以整套 /system/framework
# 一起换（dobox 的 jar 配 dobox 的 boot image，自洽）。只 A15 走这步；A14/16 不动。
#
# 用法（root）：bash host/build-framework-image.sh <base镜像> 15
set -euo pipefail
log() { printf '[fw] %s\n' "$*"; }
die() { printf '[fw] 错误：%s\n' "$*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die "请用 root 运行"
command -v docker >/dev/null || die "没有 docker"
base=${1:?用法：build-framework-image.sh <base镜像> [ver]}
ver=${2:-$(echo "$base" | grep -oE ':[0-9]+' | tr -d ':' | head -c2)}
out="${base}-fw"
if [ "$ver" != 15 ]; then log "ver=$ver 非15，不 graft 框架，直接返回 base"; echo "$base"; exit 0; fi

PP_BUILD=${PP_BUILD:-/mmc/pp-build}
TOOLS="$PP_BUILD/tools"; mkdir -p "$TOOLS"
[ -f "$TOOLS/lpunpack.py" ] || curl -sL -o "$TOOLS/lpunpack.py" \
  https://raw.githubusercontent.com/unix3dgforce/lpunpack/master/lpunpack.py
export TMPDIR="${TMPDIR:-$PP_BUILD/tmp}"; mkdir -p "$TMPDIR"
W=$(mktemp -d "$PP_BUILD/tmp-fwimg.XXXX")
DV=$W/m_dobox
cleanup(){ grep -q " $DV " /proc/mounts && umount "$DV" 2>/dev/null || true; rm -rf "$W"; }
trap cleanup EXIT
mkdir -p "$DV" "$W/stage"

DOBOX15="${DOBOX15:-registry.cn-guangzhou.aliyuncs.com/mytos/dobox:Q15_v3_all_202610271937}"
docker image inspect "$DOBOX15" >/dev/null 2>&1 || die "缺 dobox 镜像 $DOBOX15"
# 复用已扒的 dobox system.img（doboxchk/lp），没有就从 super.img 现扒
SYSIMG="$PP_BUILD/doboxchk/lp/system.img"
if [ ! -s "$SYSIMG" ]; then
  log "从 dobox super.img 扒 system（$DOBOX15）"
  dcid=$(docker create "$DOBOX15"); docker cp "$dcid:/super.img" "$W/dobox-super.img"; docker rm "$dcid" >/dev/null
  mkdir -p "$W/dobox-lp"; python3 -I "$TOOLS/lpunpack.py" -p system "$W/dobox-super.img" "$W/dobox-lp"
  SYSIMG="$W/dobox-lp/system.img"
else
  log "复用已扒 dobox system.img：$SYSIMG"
fi
mount -o ro,loop "$SYSIMG" "$DV"
DS="$DV"; [ -d "$DV/system/framework" ] && DS="$DV/system"
[ -d "$DS/framework" ] || die "dobox system 里没 /system/framework"

log "stage dobox 整套 /system/framework（jar + boot image AOT + oat）"
mkdir -p "$W/stage/system"
cp -a "$DS/framework" "$W/stage/system/framework"
sz=$(du -sh "$W/stage/system/framework" | awk '{print $1}')
log "framework 大小 $sz；含 framework.jar/services.jar + boot-*.{art,oat} + oat/"
umount "$DV" 2>/dev/null || true

log "docker build $out（整体覆盖 /system/framework）"
{
  echo "FROM $base"
  echo "COPY stage/system/framework /system/framework"
  echo "LABEL prism.framework=dobox"
} > "$W/Dockerfile"
(cd "$W" && docker build -q -t "$out" .) >/dev/null
log "完成：$out"
echo "$out"
