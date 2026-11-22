#!/usr/bin/env python3
"""镜像里模拟器字样的文件名换成像真机的（踩坑 #75）。在 build-wifi-image.sh 里、docker build 之前对 $B/vendor 跑。

App 能列 /vendor/bin/hw、/vendor/lib64、/vendor/etc/init、/dev（这台没 SELinux），看到 goldfish / cuttlefish / ranchu
（谷歌模拟器的代号）就知道是模拟器。改的只是名字，程序本身不动：
  - 电话：libgoldfish-rild → mtkfusionrild（联发科手机电话程序的真名），rild_goldfish.rc → vendor.ril.rc；
    libcuttlefish*.so → libmtk-fusion*.so：所有 vendor 程序里引用库名的字符串（依赖表、SONAME、dlopen 的名字）
    "libcuttlefish" → "libmtk-fusion"，**等长**替换，文件结构不变。函数名（cuttlefish::…）不动：符号有哈希表，改了就找不到。
  - 安卓 16 电话 HAL 的属性名 vendor.qemu.vport.modem → vendor.rild.vport.modem（等长，踩坑 #127）。
  - 传感器：android.hardware.sensors@2.1-impl.ranchu.so → …impl.mtk.so（hals.conf 跟着改）；
    驱动里打开的管道 /dev/goldfish_pipe_dprctd → /dev/hwmsensor、/dev/goldfish_pipe → /dev/sensorlist
    （联发科手机真实的传感器设备名；C 字符串，后面补 0，长度不变）。小助手按新名字放伪终端（镜像标签 prism.names=1）。

用法：rename-emu.py <build 目录里的 vendor>
"""
import os
import sys

V = sys.argv[1]
LIB_OLD, LIB_NEW = b"libcuttlefish", b"libmtk-fusion"
assert len(LIB_OLD) == len(LIB_NEW)
# 先替换长的（短的是长的前缀）
PIPES = [(b"/dev/goldfish_pipe_dprctd\0", b"/dev/hwmsensor\0"), (b"/dev/goldfish_pipe\0", b"/dev/sensorlist\0")]
# 安卓 16 电话 HAL 读基带设备路径的属性名（踩坑 #127）：App getprop 能看到「qemu」→ 换成等长的 rild 字样，
# init.radio.rc（host/ril/init.radio.ranchu.rc）里 setprop 用新名字
PROPS = [(b"vendor.qemu.vport.modem", b"vendor.rild.vport.modem")]
assert all(len(o) == len(n) for o, n in PROPS)


def pad(new, old):
    assert len(new) <= len(old)
    return new + b"\0" * (len(old) - len(new))


def patch_file(path, pairs):
    b = open(path, "rb").read()
    if not b.startswith(b"\x7fELF"):
        return 0
    n = 0
    for old, new in pairs:
        c = b.count(old)
        if c:
            b = b.replace(old, new)
            n += c
    if n:
        open(path, "wb").write(b)
    return n


def mv(old, new):
    if os.path.exists(old):
        os.rename(old, new)
        print(f"  {os.path.relpath(old, V)} → {os.path.basename(new)}")
        return True
    return False


changed = 0
# 1. 所有 vendor ELF 里的库名、管道路径
for d in ("bin", "bin/hw", "lib64", "lib64/hw"):
    p = os.path.join(V, d)
    if not os.path.isdir(p):
        continue
    for name in os.listdir(p):
        f = os.path.join(p, name)
        if os.path.isfile(f) and not os.path.islink(f):
            n = patch_file(f, [(LIB_OLD, LIB_NEW)] + [(o, pad(nw, o)) for o, nw in PIPES] + PROPS)
            if n:
                print(f"  改字符串 {n} 处：{os.path.relpath(f, V)}")
                changed += n
# 2. 文件名
lib = os.path.join(V, "lib64")
if os.path.isdir(lib):
    for name in os.listdir(lib):
        if name.startswith("libcuttlefish"):
            mv(os.path.join(lib, name), os.path.join(lib, "libmtk-fusion" + name[len("libcuttlefish"):]))
hw = os.path.join(V, "bin/hw")
if mv(os.path.join(hw, "libgoldfish-rild"), os.path.join(hw, "mtkfusionrild")):
    rc_old, rc_new = os.path.join(V, "etc/init/rild_goldfish.rc"), os.path.join(V, "etc/init/vendor.ril.rc")
    if os.path.exists(rc_old):
        s = open(rc_old).read().replace("/vendor/bin/hw/libgoldfish-rild", "/vendor/bin/hw/mtkfusionrild")
        open(rc_new, "w").write(s)
        os.remove(rc_old)
        print("  rild_goldfish.rc → vendor.ril.rc")
# 安卓 16 的原生 AIDL 电话 HAL（踩坑 #121）：二进制、启动脚本、VINTF 声明都改成 mtk 字样
if mv(os.path.join(hw, "android.hardware.radio-service.ranchu"), os.path.join(hw, "android.hardware.radio-service.mtk")):
    rc_old, rc_new = os.path.join(V, "etc/init/android.hardware.radio.ranchu.rc"), os.path.join(V, "etc/init/android.hardware.radio.mtk.rc")
    if os.path.exists(rc_old):
        s = open(rc_old).read().replace("radio-service.ranchu", "radio-service.mtk").replace("vendor.radio-ranchu", "vendor.radio-mtk")
        open(rc_new, "w").write(s)
        os.remove(rc_old)
        print("  android.hardware.radio.ranchu.rc → android.hardware.radio.mtk.rc")
    mv(os.path.join(V, "etc/vintf/manifest/android.hardware.radio.ranchu.xml"), os.path.join(V, "etc/vintf/manifest/android.hardware.radio.mtk.xml"))
sens_old = os.path.join(V, "lib64/hw/android.hardware.sensors@2.1-impl.ranchu.so")
if mv(sens_old, os.path.join(V, "lib64/hw/android.hardware.sensors@2.1-impl.mtk.so")):
    conf = os.path.join(V, "etc/sensors/hals.conf")
    if os.path.exists(conf):
        s = open(conf).read().replace("impl.ranchu.so", "impl.mtk.so")
        open(conf, "w").write(s)
# 3. 检查：还剩什么文件名带模拟器字样
left = []
for root, _, files in os.walk(V):
    for name in files:
        if any(k in name.lower() for k in ("goldfish", "cuttlefish", "ranchu", "qemu")):
            left.append(os.path.relpath(os.path.join(root, name), V))
print(f"改了 {changed} 处字符串；还带模拟器字样的文件名：{left or '没有'}")
