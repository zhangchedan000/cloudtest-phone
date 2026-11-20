#!/usr/bin/env python3
# 算 Mali 图形栈的 NEEDED 依赖闭包，把 sphal/vendor namespace 看不到的
# 非 VNDK-SP/LLNDK 库（HAL 接口库、libdmabufheap、libgralloctypes、
# arm.graphics 等）从库池补到 graft 的 vendor/lib64，并保证都是 64 位。
#
# 为什么要补：vendor/sphal namespace 只看得到 /vendor/lib64 下的库 + 系统的
# VNDK-SP/LLNDK 白名单。libGLES_mali 依赖的 HAL 接口库、libdmabufheap 等在
# system 里但不在白名单，dlopen 会 "library not found in namespace sphal"，
# 所以要把它们复制进 vendor/lib64 让 vendor namespace 自己能找到。
#
# 用法: graft-deps.py <graft_vendor_dir> <库池1> [库池2 ...] <roots逗号分隔>
#   库池：Q vendor rdump 目录（同源，优先）、redroid 解压出的 lib64 目录（兜底）
#   roots：Mali 核心文件相对 graft_vendor_dir 的路径，逗号分隔
import subprocess, os, shutil, sys

GRAFT = sys.argv[1]
POOLS = sys.argv[2:-1]
ROOTS = sys.argv[-1].split(',')

# sphal/vendor namespace 从 system 能拿到的，只有 LLNDK 白名单（+ bionic/linker）。这些
# 不要复制进 vendor（它们由 linker 的 sphal→default 链接提供；复制反而可能双份冲突）。
# 其余一律复制进 vendor/lib64。
#
# 关键（A14→A15/16 的本质差异，踩坑 #109）：**Android 15 删掉了 VNDK**。A14 时代
# libc++/libz/libbase/libutils/libcutils/libhidlbase/libunwindstack/liblzma… 这些是
# VNDK-SP，sphal 能从 system 看到；A15/16 没有 VNDK apex 了，sphal 只认 LLNDK，上面
# 这些全看不到 → Mali 栈 dlopen 时 "library not found in namespace sphal" 级联报错。
# 所以 CORE 必须缩到只剩真正的 LLNDK（见 /etc/llndk.libraries.txt + ld.config.txt 的
# namespace.sphal.link.default.shared_libs），其余 VNDK-SP 老库全部复制进 vendor/lib64。
# 对 A14 也安全：那些库在 Q14 vendor 池里就有（本就是 vendor 自带的同版本），复制=原样回填。
CORE = {
    # bionic / dynamic linker（绝不能复制进 vendor）
    'libc.so','libm.so','libdl.so','libdl_android.so','ld-android.so',
    # LLNDK（/etc/llndk.libraries.txt + sphal.link.default.shared_libs）
    'liblog.so','libnativewindow.so','libsync.so','libvndksupport.so','libbinder_ndk.so',
    'libEGL.so','libGLESv1_CM.so','libGLESv2.so','libGLESv3.so','libvulkan.so',
    'libRS.so','libRS_internal.so','libandroid_net.so','libapexsupport.so','libcgrouprc.so',
    'libmediandk.so','libselinux.so','libvendorsupport.so','libneuralnetworks.so',
    'libft2.so','libfdtrack.so','libcom.android.tethering.connectivity_native.so',
    'libclang_rt.asan-aarch64-android.so','libclang_rt.hwasan-aarch64-android.so',
    'libclang_rt.ubsan_standalone-aarch64-android.so',
}

def is64(p):
    try:
        with open(p,'rb') as f:
            f.seek(4); return f.read(1) == b'\x02'   # ELFCLASS64
    except Exception:
        return False

# 库池索引：库名 -> 64 位路径（第一个 64 位胜出）
pool = {}
for d in POOLS:
    for root,_,files in os.walk(d):
        for fn in files:
            if fn.endswith('.so') and fn not in pool:
                fp = os.path.join(root, fn)
                if is64(fp):
                    pool[fn] = fp

def needed(path):
    out = subprocess.run(['readelf','-d',path], capture_output=True, text=True).stdout
    return [l.split('[')[1].split(']')[0] for l in out.splitlines() if 'NEEDED' in l and '[' in l]

seen=set(); copied=[]; fixed=[]; missing=[]
stack=[os.path.join(GRAFT, r) for r in ROOTS]
while stack:
    p = stack.pop()
    if not os.path.exists(p): continue
    for n in needed(p):
        if n in seen: continue
        seen.add(n)
        if n in CORE: continue
        dst = os.path.join(GRAFT, 'lib64', n)
        if os.path.exists(dst):
            if not is64(dst) and n in pool:      # 修正误带的 32 位
                shutil.copy(pool[n], dst); os.chmod(dst, 0o644); fixed.append(n)
            stack.append(dst); continue
        if n in pool:
            shutil.copy(pool[n], dst); os.chmod(dst, 0o644); copied.append(n); stack.append(dst)
        else:
            missing.append(n)

print("补齐依赖 %d 个，修正位数 %d 个" % (len(copied), len(fixed)))
for c in sorted(copied): print("  +", c)
for c in sorted(fixed):  print("  ~", c, "(换64位)")
if missing:
    print("!! 缺失(库池里都没有):")
    for m in sorted(set(missing)): print("   ", m)
    sys.exit(1)
