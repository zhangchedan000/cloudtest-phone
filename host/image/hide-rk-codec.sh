#!/usr/bin/env bash
# 把瑞芯微 Codec2 的组件名 c2.rk.* 同长度改成 c2.mt.*（踩坑 #156）：
# App 列编解码器（MediaCodecList）时不再出现 c2.rk.*，露不出「板子是瑞芯微」。真·天玑机这里是 c2.mtk.*；
# rk→mt 是同长度（2 字节）二进制安全替换，改瑞芯微的 2 个库 + 2 个编解码器 XML。硬件编解码照常工作
# （组件只是换了个名字；实测 c2.mt.* 全部可用、画面仍走硬件 avc 编码）。
#
# 必须在 add-selinux.sh 之前跑：add-selinux 的 setfiles 会按原路径给改过的文件打对的标签
# （vendor_file / vendor_configs_file）；否则 Docker COPY 出来的新文件标签是「?」，强制 SELinux 下
# c2 服务链接不到 libcodec2_rk_store.so（linker: library not found）、整个硬件编解码起不来。
set -eu
img=$1
B=${PP_BUILD:-/mmc/pp-build}
[ "$(docker image inspect -f '{{index .Config.Labels "prism.rkcodec_hidden"}}' "$img" 2>/dev/null)" = 1 ] && { echo "[rkcodec] $img 已改过，跳过"; exit 0; }

cid=$(docker create "$img" /system/bin/sh)
W=$(mktemp -d "$B/rkcodec.XXXX"); trap 'rm -rf "$W"' EXIT
O=$W/overlay; mkdir -p "$O/vendor/lib64" "$O/vendor/etc"
got=0
for f in libcodec2_rk_store.so libcodec2_rk_component.so; do
  docker cp "$cid:/vendor/lib64/$f" "$O/vendor/lib64/$f" 2>/dev/null && got=1 || true
done
for f in media_codecs_c2_base.xml media_codecs_performance.xml; do
  docker cp "$cid:/vendor/etc/$f" "$O/vendor/etc/$f" 2>/dev/null || true
done
docker rm "$cid" >/dev/null
[ "$got" = 1 ] || { echo "[rkcodec] $img 没有瑞芯微 codec2（没加硬件编解码），跳过"; exit 0; }

# 同长度改名 c2.rk -> c2.mt（精确字节、带点，不碰库文件名里的 codec2_rk）
python3 - "$O" <<'PY'
import sys, glob
n = 0
for p in glob.glob(sys.argv[1] + "/vendor/lib64/*.so") + glob.glob(sys.argv[1] + "/vendor/etc/*.xml"):
    b = open(p, "rb").read()
    c = b.replace(b"c2.rk", b"c2.mt")
    if c != b:
        open(p, "wb").write(c)
        n += b.count(b"c2.rk")
print("[rkcodec] 改了", n, "处 c2.rk -> c2.mt")
PY

printf 'FROM %s\nCOPY overlay/ /\nLABEL prism.rkcodec_hidden=1\n' "$img" > "$W/Dockerfile"
docker build -q -t "$img" "$W" >/dev/null
echo "[rkcodec] ✓ $img：瑞芯微编解码器名 c2.rk.* 改成 c2.mt.*（下一步 add-selinux 打标签）"
