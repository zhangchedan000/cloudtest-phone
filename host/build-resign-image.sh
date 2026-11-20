#!/usr/bin/env bash
# 把一个 Redroid 镜像里所有 apk 用 AOSP testkey 统一重签，产出 <base>-resign（标签 prism.resign=1）。
#
# 为什么（踩坑 #103）：基础镜像 cnflysky/redroid-rk3588:lineage-20 用它自己的私有平台密钥签名，我们没有那把私钥。
# build-telephony-image.sh 补进去的 AOSP 电话应用（testkey 签）拿不到纯 signature 级权限
# （如 INTERACT_ACROSS_USERS_FULL，是 signature|installer，不是 privileged，ro.control_privapp_permissions=disable
# 也不给），于是 com.android.phone 构造 GsmCdmaPhone 时崩溃循环。framework-res（定义 android 包 = 平台签名）
# 是 android.uid.system，和 Settings/SystemUI 等共享 UID，要改平台签名就得整组一起换。容器没有 AVB/dm-verity，
# 签名是唯一门槛，所以把整镜像所有 apk 统一换成 testkey 后内部一致，平台签名=testkey，电话应用就能拿到纯 signature 权限。
#
# 保住 Google Play：GMS/Play（GmsCore/Phonesky/GoogleServicesFramework/GoogleRestore）用自己的共享 UID
# com.google.uid.shared，不和平台 UID 冲突；重签它们会破坏 Google 的证书校验（GoogleCertificatesRslt→Play 完整性/登录挂）。
# 所以 GOOGLE_RE 里的包一律跳过，保留它们的原厂 Google 签名。
#
# 用法（root；先跑过 build-telephony-image.sh 把 apksigner.jar + platform.pk8 准备好）：
#   bash host/build-resign-image.sh cnflysky/redroid-rk3588:lineage-20-wifi-tel
# 产物：cnflysky/redroid-rk3588:lineage-20-wifi-tel-resign —— 这就是电话(SIM)可用的正式镜像。
# 大文件目录放 $PP_BUILD（默认 /mmc/pp-build）。重签 154 个 apk 在 Q1 板子上约 5-8 分钟（eMMC+java 启动慢）。
set -euo pipefail
log() { printf '[resign] %s\n' "$*"; }
die() { printf '[resign] 错误：%s\n' "$*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die "请用 root 运行"
base=${1:?用法：build-resign-image.sh <镜像>}
out="${base}-resign"
PP_BUILD=${PP_BUILD:-/mmc/pp-build}
TOOLS="$PP_BUILD/tools"
[ -f "$TOOLS/apksigner.jar" ] && [ -f "$TOOLS/platform.pk8" ] || die "缺 apksigner.jar / platform.pk8（先跑过 build-telephony-image.sh）"
APKSIGNER="java -jar $TOOLS/apksigner.jar"
ZIPALIGN=""; command -v zipalign >/dev/null && ZIPALIGN=1
sign() {
  local a; a=$(mktemp --suffix=.apk)
  cp "$1" "$a"
  zip -q -d "$a" 'META-INF/*.SF' 'META-INF/*.RSA' 'META-INF/*.EC' 'META-INF/*.MF' >/dev/null 2>&1 || true
  if [ -n "$ZIPALIGN" ]; then local z; z=$(mktemp --suffix=.apk); zipalign -p -f 4 "$a" "$z" && mv "$z" "$a"; fi
  $APKSIGNER sign --key "$TOOLS/platform.pk8" --cert "$TOOLS/platform.x509.pem" --out "$2" "$a" 2>/dev/null
  rm -f "$a"
}
W=$(mktemp -d "$PP_BUILD/tmp-resign.XXXX")
cleanup() { rm -rf "$W"; }
trap cleanup EXIT
mkdir -p "$W/in" "$W/out"

log "导出镜像里的所有 apk（保留路径）"
cid=$(docker create "$base")
docker export "$cid" 2>/dev/null | tar -x -C "$W/in" 2>/dev/null || true
docker rm "$cid" >/dev/null
total=$(find "$W/in" -name '*.apk' | wc -l)
[ "$total" -gt 0 ] || die "没导出到 apk"
log "共 $total 个 apk，逐个用 testkey 重签…"
n=0; fail=0
# Google 应用保留原厂签名（跳过重签）——它们用 com.google.uid.shared，和平台 UID 不冲突；重签会破坏 Play 完整性校验
GOOGLE_RE='GmsCore|PrebuiltGmsCore|Phonesky|AndroidAuto|GoogleServicesFramework|GoogleLoginService|GoogleRestore|GoogleOne|Velvet|GooglePartnerSetup'
skip=0
while read -r f; do
  rel=${f#"$W/in"/}
  mkdir -p "$W/out/$(dirname "$rel")"
  if echo "$rel" | grep -qiE "$GOOGLE_RE"; then cp "$f" "$W/out/$rel"; skip=$((skip+1)); continue; fi
  if sign "$f" "$W/out/$rel"; then n=$((n+1)); else fail=$((fail+1)); log "  重签失败：$rel"; cp "$f" "$W/out/$rel"; fi
  if [ $(( (n+fail) % 20 )) = 0 ]; then log "  进度 $((n+fail))/$total"; fi
done < <(find "$W/in" -name '*.apk')
log "  重签完成 $n 成功 $fail 失败，跳过 Google 应用 $skip 个（保留原签名）"
fr=$(find "$W/out" -ipath '*framework/framework-res.apk' | head -1)
[ -n "$fr" ] || die "没有 framework-res.apk（导出不全？）"
$APKSIGNER verify --print-certs "$fr" 2>/dev/null | grep -qi "Android" || log "警告：framework-res 证书校验看着不对，继续"

log "打进镜像 $out（COPY 整棵重签树覆盖）"
printf 'FROM %s\nCOPY out/ /\nLABEL prism.resign=1\n' "$base" > "$W/Dockerfile"
(cd "$W" && docker build -q -t "$out" .) >/dev/null
log "完成：$out"
echo "$out"
