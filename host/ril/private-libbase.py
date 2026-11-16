#!/usr/bin/env python3
"""安卓 15：模拟器的电话库要的 libbase 函数，Redroid 15 自己的 vendor libbase 里没有（踩坑 #65）。

例：libcuttlefish-ril-2.so 要 android::base::Join<vector<string>, char>，Redroid 15 的没有 → rild 加载失败、反复重启。
两份 libbase 各有对方没有的函数（fmt 库版本不同），不能直接替换（会弄坏 WiFi、传感器等别的服务）。
办法：模拟器那份改名 libbas2.so（名字等长，直接改 SONAME 字符串），只让「缺函数」的那几个库改成依赖它。
它们其余的 libbase 函数照样能找到（进程里两份都在，按加载顺序找）。

用法：private-libbase.py <模拟器 libbase.so> <Redroid vendor libbase.so> <vendor/lib64> [别的目录]...
"""
import os
import shutil
import subprocess
import sys

OLD, NEW = b"\x00libbase.so\x00", b"\x00libbas2.so\x00"


def dyn(path, undefined):
    out = subprocess.run(["readelf", "-W", "--dyn-syms", path], capture_output=True, text=True).stdout
    syms = set()
    for line in out.splitlines():
        f = line.split()
        if len(f) < 8 or f[4] not in ("GLOBAL", "WEAK"):
            continue
        if (f[6] == "UND") == undefined:
            syms.add(f[7].split("@")[0])
    return syms


def needs_libbase(path):
    out = subprocess.run(["readelf", "-d", path], capture_output=True, text=True).stdout
    return "[libbase.so]" in out


def patch(path):
    b = open(path, "rb").read()
    n = b.count(OLD)
    if n == 0:
        raise SystemExit(f"{path} 里没找到 libbase.so 字符串")
    open(path, "wb").write(b.replace(OLD, NEW))
    return n


def main():
    emu, rd, dirs = sys.argv[1], sys.argv[2], sys.argv[3:]
    emu_has, rd_has = dyn(emu, False), dyn(rd, False)
    only_emu = emu_has - rd_has
    targets = []
    for d in dirs:
        for name in sorted(os.listdir(d)):
            p = os.path.join(d, name)
            if not os.path.isfile(p) or os.path.islink(p) or name in ("libbase.so", "libbas2.so"):
                continue
            if not needs_libbase(p):
                continue
            miss = dyn(p, True) & only_emu
            if miss:
                targets.append((p, len(miss)))
    if not targets:
        print("Redroid 的 libbase 够用，不用私有一份")
        return
    out = os.path.join(dirs[0], "libbas2.so")  # 第一个目录是 vendor/lib64
    shutil.copy2(emu, out)
    patch(out)
    for p, n in targets:
        patch(p)
        print(f"{os.path.basename(p)}：缺 {n} 个 libbase 函数 → 改用 libbas2.so")
    print(f"私有 libbase：{out}")


if __name__ == "__main__":
    main()
