#!/usr/bin/env bash
# 给 Redroid 镜像补上「电话框架应用」，产出 <base>-tel（标签 prism.telephony=1）。
#
# 为什么要这个（踩坑 #103）：基础镜像 cnflysky/redroid-rk3588:lineage-20 把 AOSP 电话应用整个剥掉了
# （容器没真 modem，Redroid 默认不装）——没有 com.android.phone（TeleService）就没有「phone」系统服务、
# 没有 RILJ、框架根本不去问卡状态，于是 rild/基带/电话 HAL 全配好了也「插卡不认」（OUT_OF_SERVICE、
# dumpsys isub 空、gsm.sim.* 全无）。这一层把缺的电话框架应用补回来，让下层的 RIL 真正被用起来。
#
# 补哪几个（都从谷歌官方模拟器系统镜像抠，同版本纯 AOSP，和电话 HAL 是配套的一套）：
#   - TeleService         → com.android.phone            注册「phone」服务、起 RILJ、加载运营商配置
#   - TelephonyProvider   → com.android.providers.telephony  存 APN / 短信 / 订阅信息，电话栈的底座
#   - CarrierConfig       → com.android.carrierconfig     默认运营商配置（否则 ICarrierConfigLoader 一直缺省）
#
# 签名的坑：com.android.phone 用 android:sharedUserId="android.uid.phone"（UID 1001），和基础镜像里的
# com.android.stk 同一个 UID——同 UID 的应用必须同一个密钥签。基础镜像的 Stk 是 cnflysky 自己的平台密钥签的，
# 我们没有那把私钥；所以改成：三个新应用用 AOSP testkey 签，并把基础镜像里的 Stk 也用同一把 testkey 重签，
# 保证 android.uid.phone 这组应用签名一致。框架对电话的信任主要看 UID 1001，不是看平台签名；而且镜像里
# ro.control_privapp_permissions=disable（特权权限不强制），所以 testkey（≠平台签名）也能把电话跑起来。
#
# 配套：电话 HAL（libgoldfish-rild + reference-ril，接口 1.6/AIDL）由 build-wifi-image.sh 装好；
#       基带由管家 agent/internal/modem 模拟（AT over 127.0.0.1:9600，踩坑 #65）。所以本镜像要叠在 -wifi 之上。
#
# 用法（root，装好 docker）：
#   bash host/build-telephony-image.sh cnflysky/redroid-rk3588:lineage-20-wifi          # 版本从镜像名猜
#   bash host/build-telephony-image.sh cnflysky/redroid-rk3588:lineage-20-wifi 13
# 工具：java(apksigner.jar) + zip + unzip + python3 + losetup；zipalign 没有就跳过（只影响 mmap 优化，非必需）。
#   Alpine 板子：apk add openjdk17-jre-headless zip
#   Debian 阿什本：apt-get install -y default-jre-headless zip
# 大文件目录（模拟器镜像 ~800MB、临时解包）默认放 $PP_BUILD（默认 /mmc/pp-build，板子 eMMC 根分区太小别用）。
set -euo pipefail
log() { printf '[tel] %s\n' "$*"; }
die() { printf '[tel] 错误：%s\n' "$*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die "请用 root 运行"
command -v docker >/dev/null || die "没有 docker"
base=${1:?用法：build-telephony-image.sh <redroid 镜像> [安卓版本]}
ver=${2:-$(echo "$base" | grep -oE ':[0-9]+' | tr -d ':' | head -c2)}
case "$ver" in
  13) emu=arm64-v8a-33_r02.zip ;;
  14) emu=arm64-v8a-34_r04.zip ;;
  15) emu=arm64-v8a-35_r02.zip ;;
  16) emu=arm64-v8a-36_r02.zip ;;
  *) die "不认识的安卓版本 $ver（支持 13/14/15/16）" ;;
esac
here=$(cd "$(dirname "$0")" && pwd)
out="${base}-tel"

PP_BUILD=${PP_BUILD:-/mmc/pp-build}
TOOLS="$PP_BUILD/tools"; CACHE="$PP_BUILD/cache"
mkdir -p "$TOOLS" "$CACHE"
for c in java zip unzip python3 losetup curl; do command -v "$c" >/dev/null || die "缺 $c"; done
APKSIGNER="java -jar $TOOLS/apksigner.jar"
if [ ! -f "$TOOLS/apksigner.jar" ]; then
  log "下载 android build-tools（取 apksigner.jar）"
  curl -fsSL -o "$TOOLS/bt.zip" https://dl.google.com/android/repository/build-tools_r34-linux.zip
  (cd "$TOOLS" && unzip -q -o bt.zip 'android-14/lib/apksigner.jar' && cp android-14/lib/apksigner.jar apksigner.jar && rm -rf bt.zip android-14)
fi
if [ ! -f "$TOOLS/platform.pk8" ]; then
  log "取 AOSP 平台 testkey"
  b=https://android.googlesource.com/platform/build/+/refs/tags/android-14.0.0_r1/target/product/security
  curl -fsSL "$b/platform.pk8?format=TEXT" | base64 -d > "$TOOLS/platform.pk8.part" && mv "$TOOLS/platform.pk8.part" "$TOOLS/platform.pk8"
  curl -fsSL "$b/platform.x509.pem?format=TEXT" | base64 -d > "$TOOLS/platform.x509.pem.part" && mv "$TOOLS/platform.x509.pem.part" "$TOOLS/platform.x509.pem"
fi
[ -f "$TOOLS/lpunpack.py" ] || { curl -fsSL -o "$TOOLS/lpunpack.py.part" \
  https://raw.githubusercontent.com/unix3dgforce/lpunpack/master/lpunpack.py && mv "$TOOLS/lpunpack.py.part" "$TOOLS/lpunpack.py"; }

ZIPALIGN=""; command -v zipalign >/dev/null && ZIPALIGN=1
sign() { # sign <in.apk> <out.apk>：testkey 重签（先去掉原签名；有 zipalign 就先对齐）
  local a b; a=$(mktemp --suffix=.apk); b="$1"
  cp "$1" "$a"
  zip -q -d "$a" 'META-INF/*.SF' 'META-INF/*.RSA' 'META-INF/*.EC' 'META-INF/*.MF' >/dev/null 2>&1 || true
  if [ -n "$ZIPALIGN" ]; then local z; z=$(mktemp --suffix=.apk); zipalign -p -f 4 "$a" "$z" && mv "$z" "$a"; fi
  $APKSIGNER sign --key "$TOOLS/platform.pk8" --cert "$TOOLS/platform.x509.pem" --out "$2" "$a"
  rm -f "$a"
}

# sign() 的 mktemp 默认落 /tmp（板子根分区 3.5G，会满）→ 走 PP_BUILD（/mmc 大盘）。踩坑 #110。
export TMPDIR="${TMPDIR:-${PP_BUILD:-/tmp}/tmp}"; mkdir -p "$TMPDIR"
W=$(mktemp -d "$PP_BUILD/tmp-telimg.XXXX")
V=$W/m_system; P=$W/m_product; DV=$W/m_dobox
cleanup() {
  for m in "$V" "$P" "$DV"; do grep -q " $m " /proc/mounts && umount "$m" 2>/dev/null || true; done
  for l in $(losetup -a 2>/dev/null | awk -F: "/$(basename "$W")/{print \$1}"); do losetup -d "$l" 2>/dev/null || true; done
  rm -rf "$W"
}
trap cleanup EXIT
mkdir -p "$V" "$P" "$DV" "$W/stage"

# ---- 抠电话应用 ----
if [ ! -s "$CACHE/$emu" ]; then
  log "下载模拟器系统镜像 $emu（缓存到 $CACHE）"
  curl -fsSL --retry 3 -o "$CACHE/$emu.part" "https://dl.google.com/android/repository/sys-img/android/$emu" && mv "$CACHE/$emu.part" "$CACHE/$emu"
else
  log "用已缓存的模拟器镜像 $CACHE/$emu"
fi
unzip -q -o "$CACHE/$emu" '*/system.img' -d "$W"
sysimg=$(find "$W" -name system.img | head -1)
L=$(losetup -fP --show -r "$sysimg"); sleep 1
log "lpunpack 取 system / product 逻辑分区"
mkdir -p "$W/lp"
python3 -I "$TOOLS/lpunpack.py" -p system,product "${L}p2" "$W/lp"
mount -o ro,loop "$W/lp/system.img" "$V"
mount -o ro,loop "$W/lp/product.img" "$P"

# 三个应用在模拟器里的位置各版本略有差异，用 find 稳妥地定位（system 或 product 分区都找）
# 注意 `| head -1`：head 取完第一行就关管道，find 收到 SIGPIPE，在 set -o pipefail 下
# 整条管道返回 141 → set -e 退出（A15 的 system 树更大、find 还没跑完 head 就关了，必中；
# A13/14 侥幸 find 先结束没触发）。→ 每条都加 `|| true` 兜底。
tele=$(find "$V" "$P" \( -ipath '*priv-app/TeleService*' -o -ipath '*priv-app/Telephony/*' \) -iname '*.apk' 2>/dev/null | grep -viE 'provider' | head -1 || true)
prov=$(find "$V" "$P" -ipath '*TelephonyProvider*' -iname '*.apk' 2>/dev/null | head -1 || true)
cc=$(find "$V" "$P" -ipath '*CarrierConfig*' -iname '*.apk' 2>/dev/null | head -1 || true)
[ -n "$tele" ] || die "模拟器里没找到 TeleService（com.android.phone）"
[ -n "$prov" ] || die "模拟器里没找到 TelephonyProvider"
log "找到：TeleService=$(basename "$(dirname "$tele")")  TelephonyProvider=$(basename "$(dirname "$prov")")  CarrierConfig=${cc:+有}${cc:-无}"

# A15 专属（踩坑 #117）：模拟器 arm64-v8a-35_r02 的 TeleService 是比 redroid15 框架更新的 AOSP 构建，
# 会调框架里没有的方法（TelephonyCountryDetector.getInstance / FeatureFlags.useAlarmCallback）→
# com.android.phone 启动崩溃循环、iphonesubinfo/phone 服务不注册、App 读不到电话信息。
# 改从魔云腾 dobox Q15 取 TeleService：dobox Q15 和 redroid15 是同一套 AOSP 15 框架构建
# （/system/framework/telephony-common.jar md5 完全相同），它的 TeleService 自洽匹配、不崩。
# TelephonyProvider 也要从 dobox 取（2026-11-21 补，踩坑 #118 续）：模拟器那份的 siminfo 表少列
# （is_satellite_provisioned_for_non_ip_datagram 等），redroid15 框架往里插 SIM 记录就 SQLiteException →
# 订阅插不进去、subId=-1 → com.android.phone 崩「Subscription doesn't exist」循环。direct-dev 不插卡、
# 不写 siminfo，所以以前只在真代理下暴露。CarrierConfig 仍用模拟器的（不触发错配）。
# dobox 镜像根目录有 /super.img（和扒 Mali 同一个取法，build-mali-image.sh）。A14/A16 不需要（它们
# 模拟器 TeleService 和各自 redroid 框架恰好对得上）。
if [ "$ver" = 15 ]; then
  DOBOX15="${DOBOX15:-registry.cn-guangzhou.aliyuncs.com/mytos/dobox:Q15_v3_all_202610271937}"
  log "A15：TeleService 改从 dobox 取（$DOBOX15）"
  docker image inspect "$DOBOX15" >/dev/null 2>&1 || die "缺 dobox 镜像 $DOBOX15（先 docker pull/load；见踩坑 #117）"
  dcid=$(docker create "$DOBOX15")
  docker cp "$dcid:/super.img" "$W/dobox-super.img"
  docker rm "$dcid" >/dev/null
  mkdir -p "$W/dobox-lp"
  python3 -I "$TOOLS/lpunpack.py" -p system "$W/dobox-super.img" "$W/dobox-lp"
  mount -o ro,loop "$W/dobox-lp/system.img" "$DV"
  tele=$(find "$DV" -ipath '*priv-app/TeleService*' -iname '*.apk' 2>/dev/null | head -1 || true)
  [ -n "$tele" ] || die "dobox Q15 里没找到 TeleService"
  log "A15：用 dobox 的 TeleService=$tele"
  dprov=$(find "$DV" -ipath '*priv-app/TelephonyProvider*' -iname '*.apk' 2>/dev/null | head -1 || true)
  [ -n "$dprov" ] || die "dobox Q15 里没找到 TelephonyProvider"
  prov=$dprov
  log "A15：用 dobox 的 TelephonyProvider=$prov"
fi

mkdir -p "$W/stage/system/priv-app/TeleService" "$W/stage/system/priv-app/TelephonyProvider"
sign "$tele" "$W/stage/system/priv-app/TeleService/TeleService.apk"
sign "$prov" "$W/stage/system/priv-app/TelephonyProvider/TelephonyProvider.apk"
if [ -n "$cc" ]; then
  mkdir -p "$W/stage/system/priv-app/CarrierConfig"
  sign "$cc" "$W/stage/system/priv-app/CarrierConfig/CarrierConfig.apk"
fi

# ---- 基础镜像里的 Stk（如果有）也用 testkey 重签（和 com.android.phone 同 UID，签名必须一致）----
# cnflysky lineage 有 Stk（它自己平台密钥签的）→ 必须一起重签成 testkey，否则 UID 1001(android.uid.phone)
# 这组签名不一致。纯 AOSP redroid（14/15 官方底子）没有 Stk → 这组就只有补进去的电话应用，无冲突，跳过即可。
cid=$(docker create "$base")
STKPATH=""
for p in system/app/Stk/Stk.apk system/priv-app/Stk/Stk.apk; do
  if docker cp "$cid:/$p" "$W/Stk-orig.apk" 2>/dev/null; then STKPATH=$p; break; fi
done
docker rm "$cid" >/dev/null
if [ -n "$STKPATH" ]; then
  log "基础镜像有 Stk（$STKPATH）→ 用同一把 testkey 重签，保 android.uid.phone 签名一致"
  mkdir -p "$W/stage/$(dirname "$STKPATH")"
  sign "$W/Stk-orig.apk" "$W/stage/$STKPATH"
else
  log "基础镜像没有 Stk（纯 AOSP redroid 正常）→ android.uid.phone 组只有电话应用，无签名冲突，跳过"
fi

# ---- 打进镜像 ----
log "docker build $out"
{
  echo "FROM $base"
  echo "COPY stage/system/priv-app/TeleService/TeleService.apk /system/priv-app/TeleService/TeleService.apk"
  echo "COPY stage/system/priv-app/TelephonyProvider/TelephonyProvider.apk /system/priv-app/TelephonyProvider/TelephonyProvider.apk"
  [ -n "$cc" ] && echo "COPY stage/system/priv-app/CarrierConfig/CarrierConfig.apk /system/priv-app/CarrierConfig/CarrierConfig.apk"
  [ -n "$STKPATH" ] && echo "COPY stage/$STKPATH /$STKPATH"
  echo "LABEL prism.telephony=1"
} > "$W/Dockerfile"
(cd "$W" && docker build -q -t "$out" .) >/dev/null
log "完成：$out"
echo "$out"
