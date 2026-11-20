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

# VNDK-SP + LLNDK：vendor/sphal namespace 本就可见，不要带（带了反而可能和 system 冲突）
CORE = {'libc.so','libm.so','libdl.so','liblog.so','libc++.so','ld-android.so','libdl_android.so',
        'libnativewindow.so','libsync.so','libbinder_ndk.so','libvndksupport.so','libz.so',
        'libbase.so','libcutils.so','libhardware.so','libhidlbase.so','libutils.so','libhwbinder.so',
        'libbacktrace.so','libunwindstack.so','libziparchive.so','liblzma.so','libjsoncpp.so',
        'libprocessgroup.so','libcompiler_rt.so','libtinyxml2.so','libcrypto.so','libssl.so',
        'libnetd_client.so','libselinux.so','libpcre2.so','libpackagelistparser.so'}

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
