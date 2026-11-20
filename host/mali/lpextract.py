#!/usr/bin/env python3
# 精简 Android LP(super.img 动态分区) 解析器：列分区 / 抠出指定逻辑分区
# 用途：魔云腾 dobox 镜像的 Android 在 super.img 里（动态分区），板子没有
# lpunpack/simg2img，用这个纯 Python 解析 LP 元数据把 vendor/system 抠出来。
# 用法:
#   python3 lpextract.py <super.img>                 列出所有分区
#   python3 lpextract.py <super.img> vendor out.img  抠出 vendor 分区到 out.img
import struct, sys

SUPER = sys.argv[1]
WANT  = sys.argv[2] if len(sys.argv) > 2 else None
OUT   = sys.argv[3] if len(sys.argv) > 3 else None

RESERVED = 4096
GEO_SIZE = 4096
SECTOR   = 512

f = open(SUPER, 'rb')

# geometry @ 4096
f.seek(RESERVED)
geo = f.read(GEO_SIZE)
gmagic, = struct.unpack('<I', geo[0:4])
if gmagic != 0x616c4467:
    print("NOT_LP geometry magic=%#x" % gmagic); sys.exit(1)
metadata_max_size,   = struct.unpack('<I', geo[40:44])
metadata_slot_count, = struct.unpack('<I', geo[44:48])

# metadata slot0 @ reserved + 2*geometry
meta_off = RESERVED + GEO_SIZE * 2
f.seek(meta_off)
hdr = f.read(256)
hmagic, = struct.unpack('<I', hdr[0:4])
if hmagic != 0x414C5030:
    print("NOT_LP header magic=%#x @ %d" % (hmagic, meta_off)); sys.exit(1)
header_size, = struct.unpack('<I', hdr[8:12])
def desc(o): return struct.unpack('<III', hdr[o:o+12])   # offset,num,entry_size
p_off, p_num, p_es = desc(80)   # partitions table descriptor
e_off, e_num, e_es = desc(92)   # extents table descriptor

tables_start = meta_off + header_size
f.seek(tables_start)
tables = f.read(metadata_max_size)

parts = []
for i in range(p_num):
    b = tables[p_off + i*p_es : p_off + i*p_es + p_es]
    name = b[0:36].split(b'\x00')[0].decode('ascii', 'replace')
    attrs, fei, nex, gi = struct.unpack('<IIII', b[36:52])
    parts.append((name, fei, nex))
exts = []
for i in range(e_num):
    b = tables[e_off + i*e_es : e_off + i*e_es + e_es]
    num_sectors, = struct.unpack('<Q', b[0:8])
    ttype,       = struct.unpack('<I', b[8:12])     # 0=LINEAR, 1=ZERO
    tdata,       = struct.unpack('<Q', b[12:20])    # LINEAR: 起始扇区
    exts.append((num_sectors, ttype, tdata))

print("=== 分区列表 (name  size) ===")
for (name, fei, nex) in parts:
    tot = sum(exts[fei+j][0] for j in range(nex)) * SECTOR
    print("%-16s %10.1f MB  extents=%d" % (name, tot/1048576.0, nex))

if WANT and OUT:
    tgt = [p for p in parts if p[0] == WANT or p[0] == WANT + '_a']
    if not tgt:
        print("分区未找到: " + WANT); sys.exit(2)
    name, fei, nex = tgt[0]
    o = open(OUT, 'wb'); total = 0
    for j in range(nex):
        num_sectors, ttype, tdata = exts[fei+j]
        if ttype == 0:   # LINEAR
            f.seek(tdata * SECTOR)
            left = num_sectors * SECTOR
            while left > 0:
                chunk = f.read(min(1048576, left)); o.write(chunk); left -= len(chunk); total += len(chunk)
        else:            # ZERO
            o.write(b'\x00' * (num_sectors * SECTOR)); total += num_sectors * SECTOR
    o.close()
    print("抠出 %s -> %s  (%.1f MB)" % (name, OUT, total/1048576.0))
