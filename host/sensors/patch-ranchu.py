#!/usr/bin/env python3
# 给模拟器传感器 HAL 插件（android.hardware.sensors@2.1-impl.ranchu.so）打补丁（踩坑 #59）：
# 它连模拟器只走 vsock（宿主 CID 2 端口 5000），失败就放弃；容器里 vsock 连到的是云厂商的虚拟机管理程序，没法接管。
# 把 qemu_pipe_open_ns 里「走 vsock」那条分支的第一条指令（mov w0,#0x1388）改成跳到「打开 /dev/goldfish_pipe_dprctd」那段。
# 按指令特征找位置（不写死地址），找不到或不唯一就报错退出，不会乱改。
# 另外把模拟器的传感器名字（Goldfish ...）和厂商（The Android Open Source Project）原地换成普通名字（只能等长或更短，后面补 0）。
# 注意：各机型真实的传感器芯片名要真机 dump 才知道，这里先用不带模拟器痕迹的通用名字（踩坑 #59）。
# 用法：python3 patch-ranchu.py <so 文件>
import struct, sys

p = sys.argv[1]
b = bytearray(open(p, 'rb').read())
if b[:4] != b'\x7fELF' or b[4] != 2:
    sys.exit('不是 64 位 ELF')
phoff = struct.unpack_from('<Q', b, 0x20)[0]
phnum = struct.unpack_from('<H', b, 0x38)[0]
segs = []
for i in range(phnum):
    t, fl, off, va, pa, fs, ms, al = struct.unpack_from('<IIQQQQQQ', b, phoff + 56 * i)
    if t == 1 and fl & 1:  # 可执行的 LOAD 段
        segs.append((off, va, fs))
MOV_5000 = 0x52827100            # mov w0, #0x1388
def ins(o): return struct.unpack_from('<I', b, o)[0]
def is_mov_w1_wreg(x): return (x & 0xffe0ffff) == 0x2a0003e1   # mov w1, wN（orr w1, wzr, wN）
def reg_of_mov(x): return (x >> 16) & 0x1f
def is_bl(x): return (x & 0xfc000000) == 0x94000000
def is_adrp_x0(x): return (x & 0x9f00001f) == 0x90000000
def is_add_x0_x0(x): return (x & 0xffc003ff) == 0x91000000
already = 0
cands = []
for off, va, fs in segs:
    for o in range(off, off + fs - 8, 4):
        x = ins(o)
        if x == MOV_5000 and is_mov_w1_wreg(ins(o + 4)) and is_bl(ins(o + 8)):
            r = reg_of_mov(ins(o + 4))
            # 往后 0x100 字节内找：adrp x0 / add x0,x0 / mov w1,同一个寄存器 / bl（就是 open(路径, flags)）
            for t in range(o + 12, o + 0x100, 4):
                if is_adrp_x0(ins(t)) and is_add_x0_x0(ins(t + 4)) and is_mov_w1_wreg(ins(t + 8)) \
                        and reg_of_mov(ins(t + 8)) == r and is_bl(ins(t + 12)):
                    cands.append((o, t))
                    break
        # 已经打过补丁：b 到 adrp x0 那段
        if (x & 0xfc000000) == 0x14000000 and is_mov_w1_wreg(ins(o + 4)) and is_bl(ins(o + 8)):
            d = (x & 0x3ffffff) * 4
            if 0 < d < 0x100 and is_adrp_x0(ins(o + d)) and is_add_x0_x0(ins(o + d + 4)):
                already += 1
NAMES = {
    'Goldfish 3-axis Accelerometer': 'Accelerometer',
    'Goldfish 3-axis Accelerometer Uncalibrated': 'Accelerometer Uncalibrated',
    'Goldfish 3-axis Gyroscope': 'Gyroscope',
    'Goldfish 3-axis Gyroscope (uncalibrated)': 'Gyroscope Uncalibrated',
    'Goldfish 3-axis Magnetic field sensor': 'Magnetometer',
    'Goldfish 3-axis Magnetic field sensor (uncalibrated)': 'Magnetometer Uncalibrated',
    'Goldfish Proximity sensor': 'Proximity Sensor',
    'Goldfish Light sensor': 'Light Sensor',
    'Goldfish Ambient Temperature sensor': 'Ambient Temperature',
    'Goldfish Pressure sensor': 'Pressure Sensor',
    'Goldfish Humidity sensor': 'Humidity Sensor',
    'Goldfish Orientation sensor': 'Orientation',
    'The Android Open Source Project': 'MTK',
}
renamed = 0
for old, new in NAMES.items():
    assert len(new) <= len(old)
    pat = b'\0' + old.encode() + b'\0'
    i = b.find(pat)
    if i >= 0 and b.find(pat, i + 1) < 0:
        b[i + 1:i + 1 + len(old)] = new.encode() + b'\0' * (len(old) - len(new))
        renamed += 1
if renamed:
    open(p, 'wb').write(b)
    print('改了 %d 个名字' % renamed)
if not cands and already == 1:
    print('已经打过补丁'); sys.exit(0)
if not cands and b'vsock' not in b:
    print('这一版不走 vsock（老版本直接开管道文件），不用打补丁'); sys.exit(0)
if len(cands) != 1:
    sys.exit('找到 %d 处，不是正好 1 处，不改' % len(cands))
o, t = cands[0]
struct.pack_into('<I', b, o, 0x14000000 | ((t - o) // 4))
open(p, 'wb').write(b)
print('已打补丁：文件偏移 0x%x → 跳到 0x%x（打开管道文件）' % (o, t))
