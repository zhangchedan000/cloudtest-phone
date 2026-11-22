#!/usr/bin/env python3
"""核对模块用到的每个内核符号：我们按公开源码编出来的 CRC（模块 .mod.c 里）和板子内核的（Module.symvers.board）是不是一样。
全一样才说明结构布局一致、模块能安全加载；有不一样的列出来，退出码 1。
用法：compare-crcs.py <模块的 .mod.c> <Module.symvers.board>
"""
import re
import sys

mine = dict((s, int(c, 16)) for c, s in re.findall(r'\{\s*(0x[0-9a-f]+),\s*"([^"]+)"\s*\}', open(sys.argv[1]).read()))
board = {}
for line in open(sys.argv[2]):
    f = line.rstrip("\n").split("\t")
    board[f[1]] = int(f[0], 16)
if not mine:
    print("✗ .mod.c 里没有符号版本（模块没按完整内核编），不能用")
    sys.exit(2)
bad = [(s, c, board.get(s)) for s, c in sorted(mine.items()) if board.get(s) != c]
print(f"模块用到 {len(mine)} 个内核符号，CRC 一致 {len(mine) - len(bad)} 个，不一致 / 板子没有 {len(bad)} 个")
for s, c, bc in bad:
    print(f"  {s}: 我们 0x{c:08x}  板子 {('0x%08x' % bc) if bc is not None else '没有这个符号'}")
sys.exit(1 if bad else 0)
