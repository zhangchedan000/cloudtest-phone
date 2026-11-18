#!/usr/bin/env python3
# 就地改二进制 AndroidManifest.xml 里的 targetSdkVersion（4 字节整数，不改文件大小，改完要重签 APK）。
# 为什么：模拟器里抠出来的 AOSP 短信 App（com.android.messaging）targetSdk=24，安卓 14 会弹
# 「此应用为旧版安卓构建」的提示（破绽+烦人）。把 targetSdk 提到 30（拨号就是 30、实测不弹）即可。
# 用法: axml_targetsdk.py AndroidManifest.xml <新值>
import struct, sys
path, new = sys.argv[1], int(sys.argv[2])
d = bytearray(open(path, 'rb').read())
assert struct.unpack_from('<I', d, 0)[0] == 0x00080003, "不是二进制 AXML"
TARGET_SDK_RESID = 0x01010270  # android:targetSdkVersion 的属性资源号
resmap = []
pos, n = 8, len(d)
patched = 0
while pos + 8 <= n:
    ctype, cheader, csize = struct.unpack_from('<HHI', d, pos)
    if csize < 8:
        break
    if ctype == 0x0180:  # XML_RESOURCE_MAP：属性名索引 -> 资源号
        cnt = (csize - 8) // 4
        resmap = list(struct.unpack_from('<%dI' % cnt, d, pos + 8))
    elif ctype == 0x0102:  # START_ELEMENT
        attr_start = struct.unpack_from('<H', d, pos + 16 + 8)[0]
        attr_size = struct.unpack_from('<H', d, pos + 16 + 10)[0]
        attr_cnt = struct.unpack_from('<H', d, pos + 16 + 12)[0]
        abase = pos + 16 + attr_start
        for i in range(attr_cnt):
            a = abase + i * attr_size
            name_idx = struct.unpack_from('<I', d, a + 4)[0]
            resid = resmap[name_idx] if name_idx < len(resmap) else 0
            if resid == TARGET_SDK_RESID:
                old = struct.unpack_from('<I', d, a + 16)[0]
                struct.pack_into('<I', d, a + 16, new)
                print("targetSdkVersion %d -> %d" % (old, new))
                patched += 1
    pos += csize
if not patched:
    sys.exit("没找到 targetSdkVersion 属性")
open(path, 'wb').write(d)
