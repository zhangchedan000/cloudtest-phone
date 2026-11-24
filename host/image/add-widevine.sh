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
# 用法（root，板子上；dobox Q<源> 镜像要先 docker load，见 docs/调研）：
#   sh host/image/add-widevine.sh <镜像> <14|15|16> [apex源版本=目标版本]
# 第 3 个参数 = 从哪个 dobox Q<源> 扒 widevine apex（默认跟目标版本一样）。
# 坑：只有 dobox Q14（2026-11 构建）把 widevine「展开 apex」烤进了 vendor 分区；Q15（2026-10 构建）
# 的 widevine 走它云端运行时注入，分区里扒不到（/apex、bin/hw、vintf 全无）。但 widevine HAL
# （android.hardware.drm-service.widevine + 内置 L3 OEMCrypto + 自带库）是谷歌自带、版本无关的，
# 清单声明 android.hardware.drm AIDL 版本 1（A13 起稳定，A14/15/16 通用）。所以 A15/A16 直接复用
# Q14 的这套 apex：sh add-widevine.sh <15镜像> 15 14 —— 实测 A14/A15/A16 都能读出每台唯一设备 ID。
set -eu
img=$1; ver=$2; src=${3:-$ver}
B=${PP_BUILD:-/mmc/pp-build}
log(){ echo "$(date +%T) [widevine] $*"; }
die(){ echo "[widevine] 错误：$*" >&2; exit 1; }

[ "$(docker image inspect -f '{{index .Config.Labels "prism.widevine"}}' "$img" 2>/dev/null)" = 1 ] && { log "$img 已有 widevine，跳过"; exit 0; }

STAGE=$B/widevine-q$src
APEXDIR=$STAGE/com.google.android.widevine
MANIFEST=$STAGE/com.google.android.widevine.xml
[ "$src" = "$ver" ] || log "目标 A$ver，复用 dobox Q$src 的 widevine apex（A$ver 的 dobox 分区里没有，HAL 版本无关）"
# 没扒过就从 dobox 扒（widevine 展开 apex 在 dobox 的 vendor 分区 /apex 下，vintf 清单在 /etc/vintf/manifest）
if [ ! -x "$APEXDIR/bin/hw/android.hardware.drm-service.widevine" ]; then
  dob=$(docker images --format '{{.Repository}}:{{.Tag}}' | grep -iE "dobox:Q$src" | head -1)
  [ -n "$dob" ] || die "没有 dobox Q$src 镜像（先 docker load dobox-Q$src.tar）"
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

# A15/A16 是「可更新 apex」模式（ro.apex.updatable=true）：apexd 只认签名的 .apex 文件，忽略我们放进
# /system/apex 的展开目录（A14 是「扁平 apex」模式，apexd 直接激活目录，所以 A14 不用这段）。可更新模式下
# 我们绕开 apexd，自己装一个 init .rc 到 /system/etc/init（一定会被扫描）：① 开机把 apex_manifest.pb 补进
# /apex（HAL 硬编码读 /apex/<自己名字>/apex_manifest.pb，缺了崩在 GetWVCdmVersion）；② 用正确身份
# （user media，group media mediadrm drmrpc system，seclabel hal_drm_default）从 /system/apex 的可执行路径
# 启动 HAL（/system 允许执行，不是 noexec 的 /apex tmpfs）；③ setenv LD_LIBRARY_PATH 指到 apex 的 lib64，
# 让它找到自带的 libcrypto.so。实测 A15 这样起 HAL 能初始化 L3 OEMCrypto 并读出每台唯一设备 ID。
cid=$(docker create "$img" /system/bin/sh)
docker cp "$cid:/system/build.prop" "$W/bp" 2>/dev/null || true
docker rm "$cid" >/dev/null
if grep -q '^ro.apex.updatable=true' "$W/bp" 2>/dev/null; then
  log "$img 是可更新 apex 模式（A$ver）：装 /system/etc/init/wv-compat.rc，绕开 apexd 自起 widevine HAL"
  mkdir -p "$O/system/etc/init"
  cat > "$O/system/etc/init/wv-compat.rc" <<'RC'
# Widevine 兼容启动（可更新 apex 模式绕开 apexd，踩坑 #155）：开机把 apex_manifest.pb 补进 /apex，
# 再用正确身份从 /system/apex 的可执行路径启动 drm HAL。
on post-fs-data
    # L3 CDM 的 keybox 基目录：init（root）在这里建好，标签对、且在 /data 准备好之后，App 读设备 ID 时
    # HAL（media 身份）就能在里面建 L3 文件系统。漏了这句 → HAL 报「Could not create base directories
    # for Level3FileSystem: Permission denied」→ abort（dobox 的 apex .rc 里本来就有这句，踩坑 #155）。
    mkdir /data/vendor/mediadrm 0770 media mediadrm
    restorecon_recursive /data/vendor/mediadrm
    # HAL 硬编码读 /apex/<自己名字>/apex_manifest.pb，补进 /apex（HAL 和 init 同命名空间，看得到）。
    mkdir /apex/com.google.android.widevine 0755 root root
    copy /system/apex/com.google.android.widevine/apex_manifest.pb /apex/com.google.android.widevine/apex_manifest.pb

service vendor.drm-widevine-hal /system/apex/com.google.android.widevine/bin/hw/android.hardware.drm-service.widevine
    seclabel u:r:hal_drm_default:s0
    interface aidl android.hardware.drm.IDrmFactory/widevine
    class hal
    user media
    group media mediadrm drmrpc system
    setenv LD_LIBRARY_PATH /system/apex/com.google.android.widevine/lib64
    ioprio rt 4
    task_profiles ProcessCapacityHigh
RC
  chmod 644 "$O/system/etc/init/wv-compat.rc"
fi

printf 'FROM %s\nCOPY overlay/ /\nLABEL prism.widevine=1\n' "$img" > "$W/Dockerfile"
docker build -q -t "$img" "$W" >/dev/null
log "✓ $img：加好 Widevine（apex + vintf + service_contexts）。下一步跑 add-selinux.sh 打标签。"
