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
case "$ver" in 16) full=16.0.0 ;; 15) full=15.0.0 ;; 14) full=14.0.0 ;; 13) full=13.0.0 ;; 12) full=12.0.0 ;; *) die "不支持的安卓版本 $ver（支持 12/13/14/15/16）" ;; esac

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
  git -C "$work" checkout -- . 2>/dev/null || true   # 丢弃上次运行留下的本地改动（sed/注入），每次从干净基线开始
  git -C "$work" pull --ff-only || true
fi
git -C "$work" checkout -- . 2>/dev/null || true       # 克隆分支也走一遍（幂等）

log "装 python 依赖（requests tqdm）"
# 板子 Alpine 的 python3 不带 pip → 直接用 apk 的系统包；阿什本 Debian 用 pip
if command -v apk >/dev/null; then
  apk add --no-cache py3-requests py3-tqdm >/dev/null 2>&1 || true
fi
if ! python3 -c 'import requests, tqdm' 2>/dev/null && [ -f "$work/requirements.txt" ]; then
  python3 -m pip install -q --break-system-packages -r "$work/requirements.txt" 2>/dev/null \
    || python3 -m pip install -q -r "$work/requirements.txt" 2>/dev/null || true
fi
python3 -c 'import requests, tqdm' 2>/dev/null || die "python 依赖 requests/tqdm 装不上（Alpine：apk add py3-requests py3-tqdm）"

# redroid.py 的 -a choices 没列全（如 15/16 都没列）。把 $full 稳妥地插在 choices=[ 之后。
grep -q "'$full'" "$work/redroid.py" || sed -i "s/choices=\[/choices=['$full', /" "$work/redroid.py"

# redroid-script 把 GApps 解压目录硬编码在 /tmp、下载缓存在 ~/.cache（板子 eMMC 根分区太小→No space）
# → 把 stuff/*.py 的 /tmp 和下载缓存都挪到 PP_BUILD（大盘）
RSTMP="${PP_BUILD:-/opt/prism-phone}/rs-tmp"; mkdir -p "$RSTMP"
sed -i "s#\"/tmp/#\"$RSTMP/#g" "$work"/stuff/*.py
export XDG_CACHE_HOME="${PP_BUILD:-/opt/prism-phone}/rs-cache"; mkdir -p "$XDG_CACHE_HOME"

# MindTheGapps 源（踩坑 #114）：redroid-script 自带的 s1204IT/MindTheGappsBuilder 只更到 15。
# 更高版本（安卓 16+）要从【官方 MindTheGapps 组织】的 <ver>-arm64 仓库取最新 release。
# 做法：取官方 arm64 zip 直链 → 下到 redroid-script 的下载缓存（固定名 mindthegapps.zip）→
# 算 md5 → 把该版本的 dl_links 条目注入 stuff/mindthegapps.py。这样 redroid.py 跑到下载那步，
# 发现缓存已存在且 md5 匹配（general.py download() 的逻辑），直接跳过下载、解包叠进镜像。
# redroid 是 64only，务必取 arm64 包。
if ! grep -q "\"$full\"" "$work/stuff/mindthegapps.py"; then
  log "redroid-script 自带源没有 MindTheGapps $full → 从官方 MindTheGapps/$full-arm64 取"
  api="https://api.github.com/repos/MindTheGapps/$full-arm64/releases/latest"
  url=$(curl -fsSL --max-time 60 -H "Accept: application/vnd.github+json" "$api" \
        | python3 -c 'import sys,json;d=json.load(sys.stdin);print(next(a["browser_download_url"] for a in d.get("assets",[]) if a["name"].endswith(".zip")))' 2>/dev/null) || url=""
  [ -n "$url" ] || die "官方 MindTheGapps/$full-arm64 没取到 zip 直链（该版本可能还没发布）"
  log "官方包：$url"
  dldir="$XDG_CACHE_HOME/redroid/downloads"; mkdir -p "$dldir"
  # 用【版本专属】缓存名，不能用 redroid-script 的通用名 mindthegapps.zip——那个会被不同
  # 版本的构建互相覆盖，复用它会把别的版本的包当成本版本（踩过：A15 的缓存被当成 16）。
  vzip="$dldir/mindthegapps-$full-arm64.zip"
  if [ ! -s "$vzip" ]; then
    log "下载 MindTheGapps $full（约 500MB，缓存到大盘）……"
    curl -fL --retry 3 --max-time 1800 -o "$vzip.part" "$url" && mv "$vzip.part" "$vzip" || die "下载 MindTheGapps $full 失败"
  else
    log "用已缓存的 $vzip"
  fi
  md5=$(md5sum "$vzip" | awk '{print $1}')
  cp -f "$vzip" "$dldir/mindthegapps.zip"   # 覆盖成 redroid-script 读的通用名（本版本的）
  log "md5=$md5，注入 stuff/mindthegapps.py 的 dl_links"
  python3 - "$work/stuff/mindthegapps.py" "$full" "$url" "$md5" <<'PY'
import sys
f,ver,url,md5=sys.argv[1:5]
s=open(f).read()
if f'"{ver}"' not in s:
    entry=('        "%s": {\n            "arm64": [\n                "%s",\n                "%s",\n            ],\n        },\n' % (ver,url,md5))
    s=s.replace("dl_links = {\n","dl_links = {\n"+entry,1)
    open(f,"w").write(s); print("injected",ver)
PY
fi

log "构建 $image（安卓 $full + MindTheGapps），这步较慢……"
( cd "$work" && python3 redroid.py -a "$full" -mtg -c docker ) || die "构建失败（看上面的日志；常见原因：下载 GApps 包的网络不通）"

docker image inspect "$image" >/dev/null 2>&1 || die "构建跑完了但没找到镜像 $image，去 docker images 看实际产出的标签"

log "完成：$image"
log "把下面这段加到 /etc/prism-phone/agent.env 的 AGENT_ARGS 后面，然后 systemctl restart prism-phone-agent："
echo "    -gapps-images ${ver}=${image}"
