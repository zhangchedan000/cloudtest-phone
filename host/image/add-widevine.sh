#!/usr/bin/env bash
# 给镜像加 Widevine DRM（踩坑 #155，「借鉴魔云腾、超越魔云腾」之一）。
#
# 从魔云腾 dobox Q<ver> 扒出它的 widevine「展开 apex」（整套 android.hardware.drm-service.widevine，
# AIDL 的 DRM HAL + 内置 L3 OEMCrypto + 测试 keybox），放进镜像的 /system/apex/，让 apexd 把它当成
# 正规模块挂载注册（dm-verity loop ext4，允许运行；/apex tmpfs 是 noexec，所以必须走 apexd 挂载，
# 不能当普通 /vendor 文件放，否则服务读不到自己的 apex_manifest、崩在 GetWVCdmVersion）。
#
# 之后手机里 App 的 MediaDrm(widevine) 就能用：每台手机自己的 /data 下由 L3 CDM 生成「每台一个、
# 互不相同」的设备 ID + keybox（/data/vendor/mediadrm），天然每台唯一 —— 正好过风控的「跨机关联」。
#
# 配套（都要有，否则强制 SELinux 下起不来 / App 够不到 / 读不出 ID）：
#   1) 本脚本：apex + vintf 清单（/vendor/etc/vintf）+ service_contexts 一条（IDrmFactory/widevine→hal_drm_service）；
#   2) add-selinux.sh：给 apex 的二进制打 hal_drm_default_exec、库打 system_lib_file（kv 列表里已加），
#      selinux-rules.txt 里的 Widevine 几条规则；本脚本必须在 add-selinux.sh 之前跑；
#   3) 管家 ensureMediaDrmDir（agent/internal/phones/widevine.go）：开机建 /data/vendor/mediadrm
#      （强制 SELinux 下 redroid 的 init 建不出这个目录，CDM 就报 cdmError 331、读不出设备 ID）。
#
# 用法（root，板子上；dobox Q<ver> 镜像要先 docker load，见 docs/调研）：
#   sh host/image/add-widevine.sh <镜像> <14|15|16>
set -eu
img=$1; ver=$2
B=${PP_BUILD:-/mmc/pp-build}
log(){ echo "$(date +%T) [widevine] $*"; }
die(){ echo "[widevine] 错误：$*" >&2; exit 1; }

[ "$(docker image inspect -f '{{index .Config.Labels "prism.widevine"}}' "$img" 2>/dev/null)" = 1 ] && { log "$img 已有 widevine，跳过"; exit 0; }

STAGE=$B/widevine-q$ver
APEXDIR=$STAGE/com.google.android.widevine
MANIFEST=$STAGE/com.google.android.widevine.xml
# 没扒过就从 dobox 扒（widevine 展开 apex 在 dobox 的 vendor 分区 /apex 下，vintf 清单在 /etc/vintf/manifest）
if [ ! -x "$APEXDIR/bin/hw/android.hardware.drm-service.widevine" ]; then
  dob=$(docker images --format '{{.Repository}}:{{.Tag}}' | grep -iE "dobox:Q$ver" | head -1)
  [ -n "$dob" ] || die "没有 dobox Q$ver 镜像（先 docker load dobox-Q$ver.tar）"
  LP=$B/tools/lpunpack.py; [ -f "$LP" ] || die "缺 $LP（先跑一次 host/image/add-hwcodec.sh 会下载）"
  command -v debugfs >/dev/null || die "缺 debugfs（apk add e2fsprogs-extra）"
  W=$(mktemp -d "$B/wvx.XXXX")
  log "从 $dob 扒 widevine apex + vintf 清单"
  cid=$(docker create "$dob"); docker cp "$cid:/super.img" "$W/super.img"; docker rm "$cid" >/dev/null
  python3 -I "$LP" -p vendor "$W/super.img" "$W" >/dev/null
  mkdir -p "$STAGE"
  debugfs -R "rdump /apex/com.google.android.widevine $STAGE" "$W/vendor.img" >/dev/null 2>&1 || true
  debugfs -R "dump /etc/vintf/manifest/com.google.android.widevine.xml $MANIFEST" "$W/vendor.img" >/dev/null 2>&1 || true
  rm -rf "$W"
  [ -x "$APEXDIR/bin/hw/android.hardware.drm-service.widevine" ] || die "没扒到 widevine 服务二进制"
fi
[ -f "$MANIFEST" ] || die "没扒到 vintf 清单（$MANIFEST）"

# apex 的 init .rc 补 seclabel（尽量让服务进 hal_drm_default 域；进不去会落 permissive kernel 域，都能用）
RC=$(ls "$APEXDIR"/etc/*.rc 2>/dev/null | head -1)
[ -n "$RC" ] && ! grep -q seclabel "$RC" && python3 - "$RC" <<'PY'
import sys; f=sys.argv[1]; o=[]
for l in open(f).read().splitlines():
    o.append(l)
    if l.strip().startswith("service vendor.drm-widevine-hal"): o.append("    seclabel u:r:hal_drm_default:s0")
open(f,"w").write("\n".join(o)+"\n")
PY

# 叠加层
W=$(mktemp -d "$B/wvimg.XXXX"); trap 'rm -rf "$W"' EXIT
O=$W/overlay
mkdir -p "$O/system/apex" "$O/vendor/etc/vintf/manifest" "$O/vendor/etc/selinux"
cp -a "$APEXDIR" "$O/system/apex/"
cp "$MANIFEST" "$O/vendor/etc/vintf/manifest/com.google.android.widevine.xml"; chmod 644 "$O/vendor/etc/vintf/manifest/com.google.android.widevine.xml"
# service_contexts：把 IDrmFactory/widevine 映射到 hal_drm_service（现成类型；否则 addService 返回 -1、崩溃循环）
cid=$(docker create "$img" /system/bin/sh)
docker cp "$cid:/vendor/etc/selinux/vendor_service_contexts" "$O/vendor/etc/selinux/vendor_service_contexts"
docker rm "$cid" >/dev/null
grep -q "IDrmFactory/widevine" "$O/vendor/etc/selinux/vendor_service_contexts" || \
  printf 'android.hardware.drm.IDrmFactory/widevine u:object_r:hal_drm_service:s0\n' >> "$O/vendor/etc/selinux/vendor_service_contexts"
chmod 644 "$O/vendor/etc/selinux/vendor_service_contexts"

printf 'FROM %s\nCOPY overlay/ /\nLABEL prism.widevine=1\n' "$img" > "$W/Dockerfile"
docker build -q -t "$img" "$W" >/dev/null
log "✓ $img：加好 Widevine（apex + vintf + service_contexts）。下一步跑 add-selinux.sh 打标签。"
