#!/bin/sh
# 给已做好的安卓镜像加 Rockchip 硬件编解码（Codec2：c2.rk.avc.encoder / hevc / vp8 编码，avc/hevc/vp9/av1… 解码），
# 从魔云腾 dobox 同版本镜像的 vendor 里取（踩坑 #133）。实时画面的 screenrecord 就会用硬件 H.264 编码，
# 不再吃满 CPU（#126：8 路软件编码 CPU 100%）。
#
# 要的东西：宿主机 /dev/mpp_service、/dev/rga（RK3588 都有，管家的特权容器里能看到）；dobox 镜像（mytos/dobox:Q<版本>…）。
# 做法：把 dobox vendor 里的 android.hardware.media.c2@1.1-service + 它依赖、而我们镜像 vendor 里没有的库（自动按 ELF 依赖算）、
# 启动脚本、seccomp 策略、media_codecs*.xml 叠到镜像 /vendor 上，补一条 VINTF 声明（hidl media.c2@1.1 IComponentStore/default）。
# 原镜像留成 <镜像>-orig-hwc；镜像加标签 prism.hwcodec=<版本>；同版本重复跑会跳过，旧版本的从 -orig-hwc 重做。
#
# ⚠️ 一定要 debug.c2.use_dmabufheaps=1：我们是特权容器，看得到 /dev/ion（魔云腾内核的老分配接口），
# Codec2 一见 /dev/ion 就用它分配输出缓冲，硬件写完 CPU 缓存不同步 → 关键帧大半是 0、解不出画面（踩坑 #133）。
# 魔云腾自己的容器没透传 /dev/ion，所以它那边没事。
#
# 用法（root，板子上）：sh host/image/add-hwcodec.sh <镜像> <安卓版本 14|15|16>
set -eu
img=$1; ver=$2
B=${PP_BUILD:-/mmc/pp-build}
log(){ echo "$(date +%T) [hwcodec] $*"; }
die(){ echo "[hwcodec] 错误：$*" >&2; exit 1; }
HWC_VER=2
cur=$(docker image inspect -f '{{index .Config.Labels "prism.hwcodec"}}' "$img" 2>/dev/null || true)
[ "$cur" = "$HWC_VER" ] && { log "$img 已经有硬件编解码（第 $HWC_VER 版），跳过"; exit 0; }
base=$img
if [ -n "$cur" ] && [ "$cur" != "<no value>" ]; then
  docker image inspect "$img-orig-hwc" >/dev/null 2>&1 || die "$img 是旧版硬件编解码（$cur），但找不到原镜像 $img-orig-hwc"
  base=$img-orig-hwc; log "$img 是旧版（$cur），从 $base 重做"
fi
command -v debugfs >/dev/null || die "缺 debugfs（apk add e2fsprogs-extra）"

# 1) dobox vendor（缓存在 $B/dobox<ver>-vendor）
DV=$B/dobox$ver-vendor
if [ ! -f "$DV/bin/hw/android.hardware.media.c2@1.1-service" ]; then
  dob=$(docker images --format '{{.Repository}}:{{.Tag}}' | grep "mytos/dobox:Q$ver" | head -1)
  [ -n "$dob" ] || die "没有魔云腾 dobox Q$ver 镜像"
  log "从 $dob 取 vendor"
  W=$(mktemp -d "$B/dobox-x.XXXX")
  cid=$(docker create "$dob"); docker cp "$cid:/super.img" "$W/super.img" >/dev/null; docker rm "$cid" >/dev/null
  python3 -I "$B/tools/lpunpack.py" -p vendor "$W/super.img" "$W" >/dev/null
  rm -rf "$DV"; mkdir -p "$DV"
  debugfs -R "rdump /bin /lib64 /etc $DV" "$W/vendor.img" >/dev/null 2>&1
  rm -rf "$W"
  [ -f "$DV/bin/hw/android.hardware.media.c2@1.1-service" ] || die "dobox Q$ver 的 vendor 里没有 media.c2 服务"
fi

# 2) 看我们镜像 vendor 里已经有哪些库
W=$(mktemp -d "$B/hwc.XXXX"); trap 'rm -rf "$W"' EXIT
cid=$(docker create "$base"); docker cp "$cid:/vendor/lib64" "$W/ours-lib64" >/dev/null; docker cp "$cid:/system/lib64" "$W/sys-lib64" >/dev/null; docker rm "$cid" >/dev/null
O=$W/overlay; mkdir -p "$O/bin/hw" "$O/lib64" "$O/etc/init" "$O/etc/seccomp_policy" "$O/etc/vintf/manifest"

# 3) 依赖闭包：dobox vendor 里有、我们 vendor 里没有的库都带上（系统库 / LLNDK 不带）
python3 - "$DV" "$W/ours-lib64" "$W/sys-lib64" "$O/lib64" <<'EOF'
import os, shutil, struct, sys
dv, ours, sysl, out = sys.argv[1:5]
def needed(p):
    b = open(p, "rb").read()
    if b[:4] != b"\x7fELF" or b[4] != 2: return []
    shoff, = struct.unpack_from("<Q", b, 0x28); shentsize, shnum = struct.unpack_from("<HH", b, 0x3a)
    secs = [struct.unpack_from("<IIQQQQIIQQ", b, shoff + i * shentsize) for i in range(shnum)]
    dyn = [s for s in secs if s[1] == 6]
    if not dyn: return []
    d = dyn[0]; strtab = secs[d[6]]; res = []
    for i in range(0, d[5], 16):
        tag, val = struct.unpack_from("<qQ", b, d[4] + i)
        if tag == 0: break
        if tag == 1:
            o = strtab[4] + val; res.append(b[o:b.index(b"\0", o)].decode())
    return res
start = [f"{dv}/bin/hw/android.hardware.media.c2@1.1-service"] + [f"{dv}/lib64/{n}" for n in os.listdir(f"{dv}/lib64") if n.startswith("libcodec2_rk")]
seen, queue, took = set(), list(start), []
while queue:
    for lib in needed(queue.pop()):
        if lib in seen: continue
        seen.add(lib)
        src = f"{dv}/lib64/{lib}"
        if not os.path.exists(src): continue                      # 系统库（libc / LLNDK）
        queue.append(src)
        if os.path.exists(f"{ours}/{lib}"): continue              # 我们 vendor 里已有同名的，用我们的
        shutil.copy2(src, f"{out}/{lib}"); took.append(lib)
for n in os.listdir(f"{dv}/lib64"):                               # rk 组件库本身（store 会 dlopen）
    if n.startswith("libcodec2_rk") and not os.path.exists(f"{out}/{n}"):
        shutil.copy2(f"{dv}/lib64/{n}", f"{out}/{n}"); took.append(n)
print(f"带上 {len(took)} 个库：" + " ".join(sorted(took)))
EOF
cp "$DV/bin/hw/android.hardware.media.c2@1.1-service" "$O/bin/hw/"
cp "$DV/etc/init/android.hardware.media.c2@1.1-service.rc" "$O/etc/init/"
for f in "$DV"/etc/seccomp_policy/android.hardware.media.c2@1.1*; do [ -f "$f" ] && cp "$f" "$O/etc/seccomp_policy/"; done
for f in "$DV"/etc/media_codecs*.xml "$DV"/etc/media_profiles*.xml; do [ -f "$f" ] && cp "$f" "$O/etc/"; done
cat > "$O/etc/vintf/manifest/android.hardware.media.c2-hw.xml" <<'EOF'
<manifest version="1.0" type="device">
    <hal format="hidl">
        <name>android.hardware.media.c2</name>
        <transport>hwbinder</transport>
        <fqname>@1.1::IComponentStore/default</fqname>
    </hal>
</manifest>
EOF
# 输出缓冲改用 dma_heap（会做缓存同步），不用 /dev/ion（见文件头）
printf 'on early-init\n    setprop debug.c2.use_dmabufheaps 1\n' > "$O/etc/init/media-c2-dmabuf.rc"
chmod 755 "$O/bin/hw/android.hardware.media.c2@1.1-service"
printf 'FROM %s\nCOPY overlay/ /vendor/\nLABEL prism.hwcodec=%s\n' "$base" "$HWC_VER" > "$W/Dockerfile"
docker image inspect "$img-orig-hwc" >/dev/null 2>&1 || docker tag "$img" "$img-orig-hwc"
docker build -q -t "$img" "$W" >/dev/null
log "✓ $img：已加硬件编解码（原镜像留在 $img-orig-hwc）"
