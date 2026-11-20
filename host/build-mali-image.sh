#!/bin/bash
# 从魔云腾 dobox 安卓镜像里扒出配套的 Mali-G610 驱动栈，graft 到官方 redroid 镜像，
# 生成带 Mali 硬件 GPU 加速的 redroid 镜像。在 RK3588 板子上跑（root）。
#
# 为什么这么做：魔云腾 dobox 镜像的 Android 有 DRM 锁死不能整体用（/init 检查 v3 env），
# 但它的 Mali 用户态驱动（和咱内核的 bifrost KMD 同版本）能提取出来，配干净的官方
# redroid 用。详见 docs/进度记录.md 第 7 节、docs/调研/魔云腾Q1固件分析.md。
#
# 默认做安卓14（当前验证通过的方案）。安卓15/16 先别用：A15 新增的
# CpuPowerStatsCollector 在容器里崩 system_server，开不了机（和 Mali 无关，纯官方
# redroid15 也崩），要先解决 aconfigd 容器适配。
#
# 用法: bash host/build-mali-image.sh [dobox镜像] [redroid基础镜像] [输出tag]
set -euo pipefail

DOBOX="${1:-registry.cn-guangzhou.aliyuncs.com/mytos/dobox:Q14_v3_all_202611161748}"
BASE="${2:-redroid/redroid:14.0.0_64only-latest}"
OUT="${3:-prism-redroid-mali:14}"

HERE="$(cd "$(dirname "$0")" && pwd)"
MALI="$HERE/mali"
W="$(mktemp -d "${TMPDIR:-/mmc/pp-build}/mali.XXXXXX" 2>/dev/null || mktemp -d)"
cleanup() { docker rm -f mali-dobox mali-rlib >/dev/null 2>&1 || true; rm -rf "$W"; }
trap cleanup EXIT

echo "[1/8] 基础镜像 $BASE"
docker image inspect "$BASE" >/dev/null 2>&1 || docker pull "$BASE"

echo "[2/8] 从 dobox 导出 super.img"
docker create --name mali-dobox "$DOBOX" >/dev/null
docker cp mali-dobox:/super.img "$W/super.img"
docker rm mali-dobox >/dev/null

echo "[3/8] 抠出 vendor 逻辑分区"
python3 "$MALI/lpextract.py" "$W/super.img" vendor "$W/vendor.img" | tail -1

echo "[4/8] rdump vendor（不挂载，debugfs 直接导出）"
mkdir -p "$W/qv"
debugfs -R "rdump / $W/qv" "$W/vendor.img" 2>/dev/null

echo "[5/8] 复制 Mali 图形栈核心文件"
G="$W/graft/vendor"
mkdir -p "$G/lib64/egl" "$G/lib64/hw" "$G/bin/hw" "$G/etc/init" "$G/etc/vintf/manifest" "$G/bin"
cp "$W/qv/lib64/egl/libGLES_mali.so"                                              "$G/lib64/egl/"
cp "$W/qv/lib64/hw/android.hardware.graphics.allocator@4.0-impl-bifrost.so"       "$G/lib64/hw/"
cp "$W/qv/lib64/hw/android.hardware.graphics.mapper@4.0-impl-bifrost.so"          "$G/lib64/hw/"
cp "$W/qv/lib64/hw/gralloc.default.so"                                            "$G/lib64/hw/" 2>/dev/null || true
cp "$W/qv/bin/hw/android.hardware.graphics.allocator@4.0-service"                 "$G/bin/hw/"
cp "$W/qv/etc/init/android.hardware.graphics.allocator@4.0-service.rc"            "$G/etc/init/"

echo "[6/8] 准备 redroid 库池 + 补依赖闭包（sphal 看不到的从库池补进 vendor/lib64）"
mkdir -p "$W/rlib"
docker create --name mali-rlib "$BASE" >/dev/null
docker export mali-rlib 2>/dev/null | tar -C "$W/rlib" -x --wildcards 'system/lib64/*.so' 'vendor/lib64/*.so' 2>/dev/null || true
docker rm mali-rlib >/dev/null
# 库池优先用 Q vendor（同源，含 odm/lib64 的 64 位 arm.graphics），兜底用 redroid system
python3 "$MALI/graft-deps.py" "$G" "$W/qv" "$W/rlib" \
  "lib64/egl/libGLES_mali.so,bin/hw/android.hardware.graphics.allocator@4.0-service,lib64/hw/android.hardware.graphics.allocator@4.0-impl-bifrost.so,lib64/hw/android.hardware.graphics.mapper@4.0-impl-bifrost.so,lib64/hw/gralloc.default.so"

echo "[7/8] 写 vintf 声明 / gpu_config.sh / Dockerfile"
cp "$MALI/manifest_mali_a14.xml" "$G/etc/vintf/manifest/manifest_mali.xml"
cp "$MALI/gpu_config.sh"         "$G/bin/gpu_config.sh"
chmod 755 "$G/bin/gpu_config.sh" "$G/bin/hw/android.hardware.graphics.allocator@4.0-service"
chmod 644 "$G"/lib64/egl/*.so "$G"/lib64/*.so "$G"/lib64/hw/*.so
cat > "$W/graft/Dockerfile" <<EOF
FROM $BASE
COPY vendor/ /vendor/
EOF

echo "[8/8] 构建镜像 $OUT"
docker build -t "$OUT" "$W/graft" | tail -2

cat <<EOF

✅ 完成: $OUT
启动（每台分独立 binder，容器 selinux 用 permissive）:
  docker run -d --privileged \\
    --device /dev/mali0 --device /dev/mpp_service \\
    -v /dev/dri:/dev/dri -v /dev/dma_heap:/dev/dma_heap \\
    -v /dev/binderN:/dev/binder -v /dev/binderM:/dev/hwbinder -v /dev/binderK:/dev/vndbinder \\
    $OUT \\
    androidboot.redroid_gpu_mode=mali androidboot.selinux=permissive androidboot.use_memfd=true

验证: docker exec <容器> dumpsys SurfaceFlinger | grep GLES   # 应显示 Mali-G610
EOF
