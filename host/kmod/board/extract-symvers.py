#!/usr/bin/env python3
"""从板子 boot 分区里的内核（魔云腾 FIT 包，内核没压缩）抽出所有导出符号的 CRC，写成 Module.symvers 格式（踩坑 #129）。
内核开了 MODVERSIONS：模块里每个用到的内核函数都带一个 CRC，和内核的对不上就拒绝加载。魔云腾没给内核源码 / 头文件，
所以从内核本身把 CRC 表抽出来，用来【核对】我们按公开源码编出来的 CRC 是不是一样（一样 = 结构布局一样，模块才安全）。

用法：extract-symvers.py <boot.img 或内核 Image> > Module.symvers.board
符号表位置从 /proc/kallsyms 读（root 能看到真实地址），所以要在板子上跑。
"""
import re
import struct
import sys

b = open(sys.argv[1], "rb").read()
img = b.find(b"ARM\x64")          # arm64 Image 头里 0x38 处是 "ARM\x64"
assert img >= 0x38, "找不到 arm64 Image"
img -= 0x38
syms = {}
for line in open("/proc/kallsyms"):
    a, _, n = line.split()[:3]
    syms[n] = int(a, 16)
TEXT = syms["_text"]
assert TEXT != 0, "kallsyms 地址是 0：要 root（kptr_restrict）"


def off(a):
    return img + (a - TEXT)


def cstr(a):
    o = off(a)
    return b[o:b.index(b"\0", o)].decode()


def table(ks, ke, cs, kind):
    for i in range((ke - ks) // 12):          # struct kernel_symbol（PREL32）：value / name / namespace 三个相对偏移
        a = ks + i * 12
        _, nm, ns = struct.unpack_from("<iii", b, off(a))
        crc = struct.unpack_from("<I", b, off(cs + i * 4))[0]
        print(f"0x{crc:08x}\t{cstr(a + 4 + nm)}\tvmlinux\t{kind}\t{cstr(a + 8 + ns) if ns else ''}")


# 先确认 boot 里的内核就是正在跑的这个
ver = open("/proc/version").read().split(" (")[0]
assert (ver.encode() + b" (") in b, f"boot 分区里的内核不是正在跑的 {ver}"
table(syms["__start___ksymtab"], syms["__stop___ksymtab"], syms["__start___kcrctab"], "EXPORT_SYMBOL")
table(syms["__start___ksymtab_gpl"], syms["__stop___ksymtab_gpl"], syms["__start___kcrctab_gpl"], "EXPORT_SYMBOL_GPL")
