#!/usr/bin/env python3
"""真机采集：把一台真手机录成「机型包」，导入后机型库就多一款（和魔云腾的采集工具一个思路，录得更全）。

准备：电脑装 adb（Android SDK platform-tools），手机打开「开发者选项 → USB 调试」，USB 连上，手机上点允许。不用 root。
用法：
  python3 collect-model.py                 # 只连了一台手机
  python3 collect-model.py -s <序列号>      # 连了好几台，指定一台（adb devices 看序列号）
  python3 collect-model.py -o <输出目录>    # 默认当前目录
产物：<品牌>_<型号>_A<安卓版本>_<日期>.zip，交给 host/models/import-model.py 导入。

录了什么（都是普通 shell 权限能读的）：
  props.txt          全部系统属性（getprop）
  buildprop/         各分区 build.prop 原文件（真实指纹、分区编译信息，导入时用 host/extract-buildprops.py 抠）
  cpu/               整个 /sys/devices/system/cpu（真链接保留成链接；读不了的文件留空）
  proc/              cpuinfo、meminfo、version、cmdline
  gpu/               dumpsys SurfaceFlinger（OpenGL 名字 / 驱动版本 / 扩展）、cmd gpu vkjson（Vulkan 设备信息）
  display.txt、sensors.txt、features.txt、libraries.txt、codecs/（media_codecs*.xml）、input.txt
  lib/libc.so        真机 libc（风控会比对系统库的编译号）
  auxv               shell 进程的辅助向量（指令集 HWCAP）
  meta.json          采集时间、工具版本、adb 序列号的哈希（不存原序列号）
不录：账号、短信、通讯录、照片、IMEI 等个人信息。
"""
import argparse, base64, datetime, hashlib, json, os, re, shlex, shutil, subprocess, sys, tempfile, zipfile

TOOL_VERSION = 1
SYS_CPU = "/sys/devices/system/cpu"


def adb(serial, *args, binary=False, timeout=120):
    cmd = ["adb"] + (["-s", serial] if serial else []) + list(args)
    r = subprocess.run(cmd, capture_output=True, timeout=timeout)
    return r.stdout if binary else r.stdout.decode("utf-8", "replace")


def sh(serial, script, timeout=120):
    # exec-out：不经过伪终端，二进制、换行都原样。adb 会把参数用空格拼起来交给手机上的 shell 再解析一遍，
    # 所以整段脚本要自己加引号（不加的话 "sh -c dumpsys SurfaceFlinger" 只执行了 dumpsys，多行脚本直接拆坏）
    return adb(serial, "exec-out", "sh -c " + shlex.quote(script), timeout=timeout)


def pick_device(serial):
    out = adb(None, "devices")
    devs = [l.split()[0] for l in out.splitlines()[1:] if l.strip().endswith("device")]
    if serial:
        if serial not in devs:
            sys.exit(f"没找到 {serial}（adb devices 里有：{devs}）")
        return serial
    if len(devs) != 1:
        sys.exit(f"连了 {len(devs)} 台手机（{devs}），用 -s 指定一台" if devs else "没有连上的手机：USB 调试开了吗？手机上点允许了吗？")
    return devs[0]


def collect_cpu(serial, dst):
    """整个 /sys/devices/system/cpu：每项一行「类型 路径」，文件接 base64 内容（读不了的留空），链接接目标。"""
    script = r'''
cd /sys/devices/system/cpu || exit 1
find . -maxdepth 6 2>/dev/null | while read -r p; do
  p=${p#./}; [ "$p" = "." ] && continue
  if [ -L "$p" ]; then echo "L|$p|$(readlink "$p")"
  elif [ -d "$p" ]; then echo "D|$p|"
  elif [ -f "$p" ]; then echo "F|$p|$(timeout 2 head -c 65536 "$p" 2>/dev/null | base64 | tr -d '\n')"
  fi
done'''
    out = sh(serial, script, timeout=600)
    n = {"F": 0, "L": 0, "D": 0}
    for line in out.splitlines():
        kind, _, rest = line.partition("|")
        path, _, val = rest.partition("|")
        if kind not in n or not path or ".." in path.split("/"):
            continue
        full = os.path.join(dst, path)
        if kind == "D":
            os.makedirs(full, exist_ok=True)
        elif kind == "L":
            os.makedirs(os.path.dirname(full), exist_ok=True)
            if not os.path.lexists(full):
                os.symlink(val, full)
        else:
            os.makedirs(os.path.dirname(full), exist_ok=True)
            with open(full, "wb") as fh:
                fh.write(base64.b64decode(val) if val else b"")
        n[kind] += 1
    return n


def main():
    ap = argparse.ArgumentParser(description="真机采集机型包")
    ap.add_argument("-s", dest="serial")
    ap.add_argument("-o", dest="out", default=".")
    a = ap.parse_args()
    if not shutil.which("adb"):
        sys.exit("找不到 adb：先装 Android SDK platform-tools，并把它加到 PATH")
    serial = pick_device(a.serial)
    prop = lambda k: sh(serial, f"getprop {k}").strip()
    brand, model, rel = prop("ro.product.brand"), prop("ro.product.model"), prop("ro.build.version.release")
    print(f"采集：{brand} {model}（安卓 {rel}，{prop('ro.build.fingerprint')}）")
    work = tempfile.mkdtemp(prefix="pp-collect-")
    w = lambda name, data: (os.makedirs(os.path.dirname(os.path.join(work, name)) or work, exist_ok=True),
                             open(os.path.join(work, name), "wb" if isinstance(data, bytes) else "w").write(data))
    try:
        w("props.txt", sh(serial, "getprop"))
        # 各分区 build.prop（和 host/extract-buildprops.py 的文件名约定一样：路径里的 / 换成 .）
        listing = sh(serial, "ls /system/build.prop /system/system/build.prop /system_ext/etc/build.prop /product/etc/build.prop "
                             "/vendor/build.prop /vendor/*_build.prop /odm/etc/build.prop /odm/etc/*build*.prop /vendor/odm/etc/build.prop "
                             "/vendor_dlkm/etc/build.prop /odm_dlkm/etc/build.prop /system_dlkm/etc/build.prop /mi_ext/etc/build.prop "
                             "/my_*/build.prop 2>/dev/null")
        nprop = 0
        for p in sorted(set(listing.split())):
            data = adb(serial, "exec-out", "cat " + shlex.quote(p), binary=True)
            if data:
                w("buildprop/" + p.lstrip("/").replace("/", "."), data); nprop += 1
        print(f"  build.prop：{nprop} 个分区文件")
        os.makedirs(os.path.join(work, "cpu"), exist_ok=True)
        n = collect_cpu(serial, os.path.join(work, "cpu"))
        print(f"  CPU 目录：{n['F']} 个文件、{n['L']} 个链接、{n['D']} 个目录")
        for f in ("cpuinfo", "meminfo", "version", "cmdline"):
            w(f"proc/{f}", sh(serial, f"cat /proc/{f} 2>&1"))
        w("auxv", adb(serial, "exec-out", "cat", "/proc/self/auxv", binary=True))
        sf = sh(serial, "dumpsys SurfaceFlinger 2>/dev/null")
        w("gpu/surfaceflinger.txt", sf)
        w("gpu/vkjson.json", sh(serial, "cmd gpu vkjson 2>&1", timeout=60))
        gles = next((l.strip() for l in sf.splitlines() if l.strip().startswith("GLES:")), "")
        print(f"  显卡：{gles[:100] or '（没读到 GLES 行）'}")
        w("display.txt", sh(serial, "wm size; wm density; dumpsys display 2>/dev/null | grep -E 'mBaseDisplayInfo|DisplayDeviceInfo|refreshRate|supportedModes' | head -40"))
        w("sensors.txt", sh(serial, "dumpsys sensorservice 2>/dev/null | head -400"))
        w("features.txt", sh(serial, "pm list features"))
        w("libraries.txt", sh(serial, "pm list libraries"))
        w("input.txt", sh(serial, "getevent -i 2>/dev/null | head -300; dumpsys input 2>/dev/null | grep -A3 'Device [0-9]*:' | head -200"))
        for p in sh(serial, "ls /vendor/etc/media_codecs*.xml /system/etc/media_codecs*.xml /odm/etc/media_codecs*.xml 2>/dev/null").split():
            w("codecs/" + p.lstrip("/").replace("/", "."), adb(serial, "exec-out", "cat " + shlex.quote(p), binary=True))
        for p in ("/apex/com.android.runtime/lib64/bionic/libc.so", "/system/lib64/libc.so"):
            data = adb(serial, "exec-out", "cat " + shlex.quote(p), binary=True)
            if data[:4] == b"\x7fELF":
                w("lib/libc.so", data); break
        meta = {"tool": "prism-phone collect-model", "version": TOOL_VERSION, "time": datetime.datetime.now().astimezone().isoformat(),
                "brand": brand, "model": model, "device": prop("ro.product.device"), "android": rel,
                "fingerprint": prop("ro.build.fingerprint"), "soc": prop("ro.soc.model") or prop("ro.board.platform"),
                "gles": gles, "serialHash": hashlib.sha256(serial.encode()).hexdigest()[:12]}
        w("meta.json", json.dumps(meta, ensure_ascii=False, indent=2))
        name = re.sub(r"[^\w.-]+", "_", f"{brand}_{model}_A{rel}_{datetime.date.today():%Y%m%d}")
        os.makedirs(a.out, exist_ok=True)
        zpath = os.path.join(a.out, name + ".zip")
        with zipfile.ZipFile(zpath, "w", zipfile.ZIP_DEFLATED) as z:
            for root, dirs, files in os.walk(work):
                for d in dirs + files:
                    full = os.path.join(root, d); rel_ = os.path.relpath(full, work)
                    if os.path.islink(full):                     # 链接：存成 zip 里的符号链接
                        info = zipfile.ZipInfo(rel_); info.external_attr = 0o120777 << 16
                        z.writestr(info, os.readlink(full))
                    elif os.path.isfile(full):
                        z.write(full, rel_)
        print(f"完成：{zpath}")
    finally:
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    main()
