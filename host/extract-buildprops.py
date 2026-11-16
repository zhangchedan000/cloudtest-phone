#!/usr/bin/env python3
"""从真机出厂 build.prop 抠出「各分区身份属性」，写进机型档案的 buildProps（踩坑 #52）。

为什么：真手机各分区的身份本来就不一样——小米的 system 分区是 Android/generic/mainline，
product 分区是 miproduct_xxx/missi，odm 分区的指纹还停在安卓 12（厂商分区不随系统升级）；
联发科的 Nothing 是 alps/mssi_64_ww_armv82。以前把 9 个分区统一改成同一个机型，反而和真机对不上。

用法：
  python3 host/extract-buildprops.py <放 *.prop 的目录>            # 打印 JSON
  python3 host/extract-buildprops.py <目录> --model profiles/models/x.json --android 14   # 直接写进档案
  加 --sku xaga：一份固件带好几个型号时（小米 vendor/xaga_build.prop、xagapro_build.prop……）只读这个型号的
  加 --region EEA：Nothing 等按地区的 build_EEA.prop 默认都不读（全球版），指定了才读

数据来源：xdaGari/tadiphone-buildprop-archive（按机型整理的真实出厂 build.prop，用 dumpyara 从官方固件抠的），
目录名形如 nothing/pacman/sys_mssi_64_ww_armv82-user-14-UP1A.231005.007-2407012308-release-keys。

规则：
  - 按安卓 init 加载顺序读（ramdisk → system → system_ext → system_dlkm → vendor → vendor_dlkm → odm → odm_dlkm
    → product → OPPO/realme 的 my_*），同名属性后读的覆盖先读的（和真机运行时一致）。
  - 只留身份相关字段（各分区 build 指纹 / id / tags / type / incremental / date.utc，
    各分区 ro.product.* 的 brand / device / manufacturer / model / name / marketname，display.id 等）。
    不碰 sdk / release / abi 这类能力字段（要和实际跑的镜像一致）；安全补丁日期也不碰：比镜像旧的话
    密钥库会认为补丁回退，已有手机存的密钥可能失效（踩坑 #52）。
  - 值带空格的跳过（启动参数遇空格截断，踩坑 #43），空值跳过。
"""
import json
import os
import re
import sys

PARTS = "system|vendor|odm|product|system_ext|vendor_dlkm|odm_dlkm|system_dlkm|bootimage"
KEEP = [
    re.compile(rf"^ro\.(({PARTS})\.)?build\.(fingerprint|id|tags|type|version\.incremental|date\.utc)$"),
    re.compile(rf"^ro\.product\.(({PARTS})\.)?(brand|device|manufacturer|model|name|marketname)$"),
    re.compile(r"^ro\.build\.(display\.id|flavor|product|host|user)$"),
]

# 文件名前缀 → 加载先后（数字小的先读）
ORDER = [
    ("ramdisk", 0), ("system.system.system_dlkm", 3), ("system.system", 1), ("system_ext", 2), ("system_dlkm", 3),
    ("vendor_boot", 0), ("vendor_dlkm", 5), ("vendor", 4), ("odm_dlkm", 7), ("odm", 6), ("product", 8), ("my_", 9),
]


def rank(name):
    if ".ramdisk." in name:
        return 0
    # 有的转储把 system 分区放在根上：system.build.prop、system.system_dlkm.etc.build.prop
    if name == "system.build.prop":
        return 1
    if name.startswith("system.system_dlkm"):
        return 3
    # 小米按机型 SKU 导入的地区配置（vendor/odm 文件末尾 import /vendor/${sku}_build.prop），紧跟在原文件之后读
    if re.match(r"^vendor\.\w+_build\.prop$", name):
        return 4.5
    if re.match(r"^odm\.etc\.\w+_build\.prop$", name):
        return 6.5
    if name.startswith("mi_ext"):
        return 8.5
    for prefix, r in ORDER:
        if name.startswith(prefix):
            return r
    return 99


SKU_FILE = re.compile(r"^(vendor|odm\.etc)\.(\w+)_build\.prop$")  # 小米按机型 SKU 的文件
REGION_FILE = re.compile(r"\.build_([A-Z]+)\.prop$")              # Nothing 等按地区的文件（build_EEA.prop）


def wanted(name, sku, region):
    """一份固件常带好几个型号 / 地区的配置，真机开机只导入自己那一份：只留指定的 SKU / 地区。"""
    m = SKU_FILE.match(name)
    if m and sku is not None and m.group(2) != sku:
        return False
    m = REGION_FILE.search(name)
    if m and m.group(1) != (region or ""):
        return False
    return True


def extract(d, sku=None, region=None):
    files = sorted((f for f in os.listdir(d) if f.endswith(".prop")), key=lambda f: (rank(f), f))
    props, skipped = {}, {}
    for f in files:
        if rank(f) == 99 or not wanted(f, sku, region):
            continue
        for line in open(os.path.join(d, f), encoding="utf-8", errors="replace"):
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            k, v = k.strip(), v.strip()
            if not any(p.match(k) for p in KEEP):
                continue
            if v == "":
                continue
            if " " in v:
                skipped[k] = v
                props.pop(k, None)
                continue
            props[k] = v
    return dict(sorted(props.items())), skipped


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(2)
    opt = lambda k: sys.argv[sys.argv.index(k) + 1] if k in sys.argv else None
    props, skipped = extract(sys.argv[1], sku=opt("--sku"), region=opt("--region"))
    if "--model" in sys.argv:
        path = sys.argv[sys.argv.index("--model") + 1]
        ver = sys.argv[sys.argv.index("--android") + 1]
        m = json.load(open(path, encoding="utf-8"))
        m.setdefault("buildProps", {})[ver] = props
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(m, fh, ensure_ascii=False, indent=2)
            fh.write("\n")
        print(f"写进 {path} 的 buildProps[{ver}]：{len(props)} 项（带空格跳过 {len(skipped)} 项）")
    else:
        print(json.dumps({"props": props, "skippedWithSpaces": skipped}, ensure_ascii=False, indent=1))


if __name__ == "__main__":
    main()
