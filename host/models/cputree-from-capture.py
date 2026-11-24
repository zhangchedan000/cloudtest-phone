#!/usr/bin/env python3
"""把真机采集的 /sys/devices/system/cpu 目录转成 CPU 模板 profiles/cpu/<名>.json（踩坑 #147）。

采集来源可以是：魔云腾机型包里的 cpu/ 目录（链接存成「内容是链接目标的普通文件」），
或者我们自己的采集脚本拷下来的目录（链接保留成真链接）。

用法：
  python3 -I host/models/cputree-from-capture.py <cpu 目录> --name mt6896 --soc MT6896 --source "说明" > profiles/cpu/mt6896.json
  加 --materialize <目录>：顺便按模板建一份目录（调试用）

规则：
  - 真机 sysfs 里的符号链接（cpuN/cpufreq、of_node、subsystem、driver、firmware_node……）还原成链接；
  - 采到的空文件（真机上普通权限读不了）内容留空，管家写的时候设成只有 root 能读（App 读得到「没权限」，和真机一样）；
  - 二进制 / 太大的文件跳过。
"""
import json, os, posixpath, sys

LINK_NAMES = {"cpufreq", "of_node", "subsystem", "driver", "firmware_node", "device", "port"}
BASE = "/sys/devices/system/cpu"


def main():
    a = sys.argv[1:]
    if not a or a[0].startswith("-"):
        print(__doc__); sys.exit(2)
    root = a[0]
    opt = lambda k, d="": a[a.index(k) + 1] if k in a else d
    t = {"name": opt("--name"), "soc": opt("--soc"), "source": opt("--source"), "files": {}, "links": {}, "dirs": []}
    for dirpath, dirnames, filenames in os.walk(root, followlinks=False):
        rel_dir = os.path.relpath(dirpath, root)
        rel_dir = "" if rel_dir == "." else rel_dir.replace(os.sep, "/")
        for d in list(dirnames):
            full = os.path.join(dirpath, d)
            rel = posixpath.join(rel_dir, d) if rel_dir else d
            if os.path.islink(full):                       # 我们自己采集的：真链接
                t["links"][rel] = os.readlink(full); dirnames.remove(d)
            elif not os.listdir(full):
                t["dirs"].append(rel)
        for f in filenames:
            full = os.path.join(dirpath, f)
            rel = posixpath.join(rel_dir, f) if rel_dir else f
            if os.path.islink(full):
                t["links"][rel] = os.readlink(full); continue
            raw = open(full, "rb").read()
            if len(raw) > 256 * 1024:
                continue
            try:
                text = raw.decode("utf-8")
            except UnicodeDecodeError:
                continue
            s = text.strip()
            # 魔云腾的包：链接存成内容是目标路径的普通文件
            if f in LINK_NAMES and s and "\n" not in s and (s.startswith("../") or s.startswith("/")):
                parent = posixpath.join(BASE, rel_dir) if rel_dir else BASE
                target = posixpath.normpath(posixpath.join(parent, s))
                if target == BASE or target.startswith(BASE + "/"):
                    s = posixpath.relpath(target, parent)  # 目录里面的：存成相对链接
                t["links"][rel] = s
                continue
            t["files"][rel] = text
    t["dirs"].sort()
    out = json.dumps(t, ensure_ascii=False, indent=0, sort_keys=True)
    print(out)
    m = opt("--materialize")
    if m:
        for d in t["dirs"]:
            os.makedirs(os.path.join(m, d), exist_ok=True)
        for p, c in t["files"].items():
            os.makedirs(os.path.dirname(os.path.join(m, p)), exist_ok=True)
            with open(os.path.join(m, p), "w") as fh:
                fh.write(c)
            os.chmod(os.path.join(m, p), 0o400 if c == "" else 0o444)
        for p, l in t["links"].items():
            os.makedirs(os.path.dirname(os.path.join(m, p)), exist_ok=True)
            if not os.path.lexists(os.path.join(m, p)):
                os.symlink(l, os.path.join(m, p))
    n_cpu = len({p.split("/")[0] for p in t["files"] if p.split("/")[0].startswith("cpu") and p.split("/")[0][3:].isdigit()})
    print(f"# {len(t['files'])} 个文件（{sum(1 for c in t['files'].values() if c == '')} 个读不了）、{len(t['links'])} 个链接、{n_cpu} 个核", file=sys.stderr)


if __name__ == "__main__":
    main()
