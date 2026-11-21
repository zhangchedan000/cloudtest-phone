#!/usr/bin/env bash
# 给 Redroid 镜像补上「正常手机该有的应用 + 桌面布局」，产出 <base>-apps（标签 prism.apps=1）。
#
# 做两件事：
#  1) 从谷歌官方模拟器系统镜像抠出 AOSP 拨号（com.android.dialer，priv-app）和短信（com.android.messaging），
#     连同拨号的特权权限白名单一起装进 /product；短信把 targetSdk 从 24 提到 30（否则安卓 14 会弹
#     「此应用为旧版安卓构建」，既是差异项又烦人，踩坑 #88）。两个 App 都是 AOSP testkey 签名，和 Redroid 同签名，
#     直接当系统应用用。
#  2) 改 Launcher3 的默认桌面布局（default_workspace_{5x5,4x4}.xml），让新机一开机桌面就摆好：
#     底栏 电话 短信 浏览器 相机 商店；桌面 设置 时钟 日历 联系人 文件 / 相册…（装了对应 App 才显示）。
#     改的是镜像里的 Launcher3QuickStep.apk（只换一个资源文件），用平台 testkey 重签（Redroid 用的就是 testkey，签名不变）。
#
# 用法（root，ARM64 宿主机，装好 docker）：
#   bash host/build-apps-image.sh redroid/redroid:14.0.0_mindthegapps-wifi        # 带谷歌版（有 Play 商店）
#   bash host/build-apps-image.sh redroid/redroid:14.0.0_64only-latest-wifi 14    # 不带谷歌版
#   第二个参数是安卓版本（默认从镜像名猜）；模拟器镜像按版本选（14→android-34）。
#
# 为什么要这么绕：ARM64 宿主机上 Google 预编译的 apktool/build-tools 是 x86 跑不了；这里只用系统自带的
# arm64 aapt2 + apt 的 zipalign + apksigner.jar（Java，跟架构无关）+ 纯 python 的 lpunpack，全程不碰 x86 工具。
set -euo pipefail
log() { printf '[apps] %s\n' "$*"; }
die() { printf '[apps] 错误：%s\n' "$*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die "请用 root 运行"
command -v docker >/dev/null || die "没有 docker（先运行 host/setup-host.sh）"

base=${1:?用法：build-apps-image.sh <redroid 镜像> [安卓版本]}
ver=${2:-$(echo "$base" | grep -oE ':[0-9]+' | tr -d ':')}
case "$ver" in
  14) emu=arm64-v8a-34_r04.zip ;;
  15) emu=arm64-v8a-35_r02.zip ;;
  16) emu=arm64-v8a-36_r02.zip ;;
  13) emu=arm64-v8a-33_r02.zip ;;
  *) die "不认识的安卓版本 $ver（支持 13/14/15/16）" ;;
esac
here=$(cd "$(dirname "$0")" && pwd)
out="${base}-apps"

# ---- 工具（幂等，缓存在 $PP_TOOLS）----
# 可移植：阿什本(Debian)默认 /opt/prism-phone/tools；板子(Alpine)用 PP_TOOLS=/mmc/pp-build/tools。
# 必需：java zip unzip python3 losetup；aapt2/zipalign 可选（只影响桌面布局和 mmap 对齐，缺了自动跳过）。
TOOLS=${PP_TOOLS:-/opt/prism-phone/tools}
BT="$TOOLS/build-tools/34.0.0"
# apksigner.jar 可能在 build-tools 里，也可能单独放（板子 /mmc/pp-build/tools/apksigner.jar）
APKJAR="$BT/lib/apksigner.jar"; [ -f "$APKJAR" ] || APKJAR="$TOOLS/apksigner.jar"
APKSIGNER="java -jar $APKJAR"
mkdir -p "$TOOLS"
if ! command -v java >/dev/null || ! command -v zip >/dev/null; then
  if command -v apt-get >/dev/null; then
    log "装工具（Debian: aapt2 / zipalign / zip / JRE / python3）"
    timeout 300 apt-get update -qq || true
    timeout 300 apt-get install -y -qq aapt zipalign zip unzip default-jre-headless python3 util-linux coreutils >/dev/null 2>&1 || true
  fi
fi
for c in java zip unzip python3 losetup; do command -v "$c" >/dev/null || die "缺必需工具 $c（Debian: apt-get install -y default-jre-headless zip；Alpine: apk add openjdk17-jre-headless zip）"; done
command -v zipalign >/dev/null || log "没有 zipalign——跳过对齐（不影响功能）"
command -v aapt2 >/dev/null || log "没有 aapt2——桌面布局那步会跳过，只装拨号/短信"
if [ ! -f "$APKJAR" ]; then
  BT="$TOOLS/build-tools/34.0.0"; APKJAR="$BT/lib/apksigner.jar"; APKSIGNER="java -jar $APKJAR"
  log "下载 android build-tools（取 apksigner.jar；zipalign/aapt2 用 apt 的 arm64 版）"
  curl -fsSL -o "$TOOLS/bt.zip" https://dl.google.com/android/repository/build-tools_r34-linux.zip
  rm -rf "$TOOLS/build-tools" && mkdir -p "$TOOLS/build-tools"
  unzip -q "$TOOLS/bt.zip" -d "$TOOLS/build-tools" && rm "$TOOLS/bt.zip"
  mv "$TOOLS/build-tools/android-14" "$BT"
fi
if [ ! -f "$TOOLS/platform.pk8" ]; then
  log "取 AOSP 平台 testkey"
  b=https://android.googlesource.com/platform/build/+/refs/tags/android-14.0.0_r1/target/product/security
  curl -fsSL "$b/platform.pk8?format=TEXT" | base64 -d > "$TOOLS/platform.pk8.part" && mv "$TOOLS/platform.pk8.part" "$TOOLS/platform.pk8"
  curl -fsSL "$b/platform.x509.pem?format=TEXT" | base64 -d > "$TOOLS/platform.x509.pem.part" && mv "$TOOLS/platform.x509.pem.part" "$TOOLS/platform.x509.pem"
fi
[ -f "$TOOLS/lpunpack.py" ] || { curl -fsSL -o "$TOOLS/lpunpack.py.part" \
  https://raw.githubusercontent.com/unix3dgforce/lpunpack/master/lpunpack.py && mv "$TOOLS/lpunpack.py.part" "$TOOLS/lpunpack.py"; }
sign() { # sign <in.apk> <out.apk>
  local a; a=$(mktemp --suffix=.apk)
  if command -v zipalign >/dev/null; then zipalign -p -f 4 "$1" "$a"; else cp "$1" "$a"; fi
  $APKSIGNER sign --key "$TOOLS/platform.pk8" --cert "$TOOLS/platform.x509.pem" --out "$2" "$a"
  rm -f "$a"
}

# aapt2 分发：默认用本机 arm64 aapt2（快）。安卓 15(API35) 的资源格式太新、本机 aapt2(2.19) 读不了，
# 这时改用 x86 容器里的新版 aapt2（build-tools r35，docker 靠 qemu 跑 x86）。
AAPT2_IMG=prism/aapt2-x86
AAPT2_MODE=native
AAPT2() {
  if [ "$AAPT2_MODE" = docker ]; then
    docker run --rm --platform linux/amd64 -v "$W:$W" -w "$W" "$AAPT2_IMG" aapt2 "$@"
  else
    aapt2 "$@"
  fi
}
# 准备 x86 新版 aapt2（首次用到才做）：装好 qemu/binfmt，下 build-tools r35 的 aapt2，做个带它的小 x86 镜像。
setup_x86_aapt2() {
  docker image inspect "$AAPT2_IMG" >/dev/null 2>&1 && return 0
  log "准备 x86 新版 aapt2（读安卓15 的新资源格式，要装 qemu + 下 build-tools r35）"
  if ! docker run --rm --platform linux/amd64 debian:12-slim true >/dev/null 2>&1; then
    timeout 300 apt-get install -y -qq qemu-user-static binfmt-support >/dev/null 2>&1 || true
    docker run --rm --privileged tonistiigi/binfmt --install amd64 >/dev/null 2>&1 || true
  fi
  docker run --rm --platform linux/amd64 debian:12-slim true >/dev/null 2>&1 || { log "docker 跑不了 x86，放弃 x86 aapt2"; return 1; }
  if [ ! -f "$TOOLS/bt35-aapt2" ]; then
    local z; z=$(mktemp -d /opt/prism-phone/tmp-bt35.XXXX)
    curl -fsSL -o "$z/bt35.zip" https://dl.google.com/android/repository/build-tools_r35_linux.zip
    unzip -q "$z/bt35.zip" -d "$z/bt35" && cp "$(find "$z/bt35" -name aapt2 | head -1)" "$TOOLS/bt35-aapt2"
    rm -rf "$z"
  fi
  [ -f "$TOOLS/bt35-aapt2" ] || { log "下 build-tools r35 失败"; return 1; }
  local d; d=$(mktemp -d /opt/prism-phone/tmp-aapt2x86.XXXX)
  cp "$TOOLS/bt35-aapt2" "$d/aapt2"
  cat > "$d/Dockerfile" <<'DF'
FROM --platform=linux/amd64 debian:12-slim
RUN apt-get update -qq && apt-get install -y -qq libc++1 && rm -rf /var/lib/apt/lists/*
COPY aapt2 /usr/local/bin/aapt2
RUN chmod +x /usr/local/bin/aapt2
DF
  (cd "$d" && docker build --platform linux/amd64 -q -t "$AAPT2_IMG" .) >/dev/null || { rm -rf "$d"; return 1; }
  rm -rf "$d"
}

# sign() 用 mktemp 签 apk，默认落 /tmp（板子 eMMC 根分区只有 3.5G，会被填满→No space）。
# 让 mktemp 走 PP_BUILD（/mmc，NVMe 大盘）。见踩坑 #110。
export TMPDIR="${TMPDIR:-${PP_BUILD:-/tmp}/tmp}"; mkdir -p "$TMPDIR"
W=$(mktemp -d "${PP_BUILD:-/opt/prism-phone}/tmp-appsimg.XXXX")
V=$W/m_system P=$W/m_product
cleanup() {
  for m in "$V" "$P"; do grep -q " $m " /proc/mounts && umount "$m"; done
  for l in $(losetup -a 2>/dev/null | awk -F: "/$(basename "$W")/{print \$1}"); do losetup -d "$l" 2>/dev/null || true; done
  rm -rf "$W"
}
trap cleanup EXIT
mkdir -p "$W/stage/product/priv-app/Dialer" "$W/stage/product/app/messaging" "$W/stage/product/etc/permissions" "$V" "$P"

# ---- 1) 抠拨号 / 短信 ----
CACHE=${PP_CACHE:-/opt/prism-phone/cache}; mkdir -p "$CACHE"
if [ ! -s "$CACHE/$emu" ]; then
  log "下载模拟器系统镜像 $emu（缓存到 $CACHE）"
  curl -fsSL --retry 3 -o "$CACHE/$emu.part" "https://dl.google.com/android/repository/sys-img/android/$emu" \
    && mv "$CACHE/$emu.part" "$CACHE/$emu"
else
  log "用已缓存的模拟器镜像 $CACHE/$emu"
fi
unzip -q -o "$CACHE/$emu" '*/system.img' -d "$W"
sysimg=$(find "$W" -name system.img | head -1 || true)
L=$(losetup -fP --show -r "$sysimg"); sleep 1
log "lpunpack 取 system / product 逻辑分区"
mkdir -p "$W/lp"
python3 -I "$TOOLS/lpunpack.py" -p system,product "${L}p2" "$W/lp"
mount -o ro,loop "$W/lp/system.img" "$V"
mount -o ro,loop "$W/lp/product.img" "$P"
dialer=$(find "$P" -ipath '*priv-app/Dialer*' -iname '*.apk' | head -1 || true)
msg=$(find "$P" -ipath '*app/messaging*' -iname '*.apk' | head -1 || true)
dxml=$(find "$P" -name 'com.android.dialer.xml' | head -1 || true)
[ -n "$dialer" ] && [ -n "$msg" ] && [ -n "$dxml" ] || die "模拟器 product 里没找到 Dialer/messaging/权限白名单"
cp "$dialer" "$W/stage/product/priv-app/Dialer/Dialer.apk"
cp "$dxml" "$W/stage/product/etc/permissions/com.android.dialer.xml"
log "短信 targetSdk 提到 30 并重签（免弹旧版提示）"
cp "$msg" "$W/msg-orig.apk"
cd "$W"
unzip -o -q msg-orig.apk AndroidManifest.xml
python3 -I "$here/apps/axml_targetsdk.py" AndroidManifest.xml 30
cp msg-orig.apk msg-patched.apk
zip -d msg-patched.apk 'META-INF/*.SF' 'META-INF/*.RSA' 'META-INF/*.MF' >/dev/null 2>&1 || true
zip -X msg-patched.apk AndroidManifest.xml >/dev/null
cd "$here"
sign "$W/msg-patched.apk" "$W/stage/product/app/messaging/messaging.apk"

# ---- 2) 改 Launcher3 默认桌面布局 ----
log "从基础镜像取 Launcher3 + framework-res"
cid=$(docker create "$base")
L3PATH=system/system_ext/priv-app/Launcher3QuickStep/Launcher3QuickStep.apk
HAVE_L3=1
docker cp "$cid:/$L3PATH" "$W/L3.apk" 2>/dev/null || HAVE_L3=0
docker cp "$cid:/system/framework/framework-res.apk" "$W/fw.apk" 2>/dev/null || HAVE_L3=0
docker rm "$cid" >/dev/null
[ "$HAVE_L3" = 1 ] || log "基础镜像没有 Launcher3QuickStep（lineage 用 Trebuchet 等别的桌面）——跳过桌面布局，只装拨号/短信"
log "编译自定义 default_workspace 并换进 Launcher3，用平台 testkey 重签"
# 改桌面布局是 best-effort：安卓 15（API 35）的资源表格式较新，系统自带的 aapt2(2.19) 读不了会崩，
# 这时就跳过布局、只保留原桌面（拨号/短信照样装）。安卓 14 实测正常。
# 注意：别靠 set -e——函数在 if 条件里时 set -e 会被忽略，aapt2 失败也不会中断。
# 每一步都显式 || return 1，并在最后验证确实换进去了。
make_launcher() {
  rm -rf "$W/wsc" "$W/wsx" "$W/ws-linked.apk" "$W/L3-mod.apk"
  mkdir -p "$W/wsc"
  for g in 5x5 4x4; do
    src="$here/apps/default_workspace_${g}.xml"
    [ -f "$src" ] || continue
    mkdir -p "$W/ws_$g/res/xml"
    cp "$src" "$W/ws_$g/res/xml/default_workspace_${g}.xml"
    cat > "$W/ws_$g/AndroidManifest.xml" <<'M'
<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="com.prism.wsbuild"><application/></manifest>
M
    AAPT2 compile "$W/ws_$g/res/xml/default_workspace_${g}.xml" -o "$W/wsc/" || return 1
  done
  AAPT2 link -I "$W/fw.apk" -I "$W/L3.apk" --manifest "$W/ws_5x5/AndroidManifest.xml" \
    -o "$W/ws-linked.apk" "$W"/wsc/*.flat || return 1
  mkdir -p "$W/wsx"
  ( cd "$W/wsx" && unzip -o -q "$W/ws-linked.apk" 'res/xml/default_workspace_*.xml' ) || return 1
  ls "$W"/wsx/res/xml/default_workspace_*.xml >/dev/null 2>&1 || return 1   # 真取到布局了才算数
  cp "$W/L3.apk" "$W/L3-mod.apk"
  zip -d "$W/L3-mod.apk" 'META-INF/*.SF' 'META-INF/*.RSA' 'META-INF/*.MF' >/dev/null 2>&1 || true
  ( cd "$W/wsx" && zip -X "$W/L3-mod.apk" res/xml/default_workspace_*.xml >/dev/null ) || return 1
  sign "$W/L3-mod.apk" "$W/stage/L3-signed.apk" || return 1
}
L3_OK=0
if [ "$HAVE_L3" != 1 ]; then
  : # 基础镜像没有 Launcher3QuickStep，跳过桌面布局
elif make_launcher >"$W/launcher.log" 2>&1; then
  L3_OK=1
else
  log "本机 aapt2 读不了这镜像的资源（安卓15 常见），改用 x86 新版 aapt2 重试…"
  if setup_x86_aapt2; then
    AAPT2_MODE=docker
    if make_launcher >"$W/launcher2.log" 2>&1; then L3_OK=1; fi
    AAPT2_MODE=native
  fi
  if [ "$L3_OK" != 1 ]; then
    log "警告：改桌面布局没成功，跳过布局、只装拨号/短信。日志：$W/launcher.log 和 launcher2.log"
    tail -3 "$W/launcher2.log" 2>/dev/null >&2 || tail -3 "$W/launcher.log" >&2 || true
  fi
fi

# ---- 3) 打进镜像 ----
log "docker build $out（桌面布局：$([ "$L3_OK" = 1 ] && echo 改了 || echo 跳过)）"
{
  echo "FROM $base"
  [ "$L3_OK" = 1 ] && echo "COPY stage/L3-signed.apk /$L3PATH"
  echo "COPY stage/product/priv-app/Dialer/Dialer.apk /product/priv-app/Dialer/Dialer.apk"
  echo "COPY stage/product/etc/permissions/com.android.dialer.xml /product/etc/permissions/com.android.dialer.xml"
  echo "COPY stage/product/app/messaging/messaging.apk /product/app/messaging/messaging.apk"
  echo "LABEL prism.apps=1 prism.appslayout=$L3_OK"
} > "$W/Dockerfile"
(cd "$W" && docker build -q -t "$out" .) >/dev/null
log "完成：$out"
echo "$out"
