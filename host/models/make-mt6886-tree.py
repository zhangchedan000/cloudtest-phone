#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""合成天玑 7200（MT6886）的 /sys/devices/system/cpu 树模板。

为什么要合成：机型库里 nothing-phone-2a、redmi-note-13-pro-plus 是天玑 7200（MT6886，
ARMv9 的 2×Cortex-A715 + 6×Cortex-A510），但手上没有真天玑 7200 的 CPU 目录采集包，
没树就只能露出板子真核（RK3588 的 A76/A55）、和「天玑7200」对不上（踩坑 #147 的那类浅层破绽）。

做法（最干净的合成）：拿一棵**真联发科树当骨架**——MT6877（redmi-22101316ucp）正好是
**2 大核 + 6 小核**、和天玑 7200 的簇结构（policy0 六小核 / policy6 两大核）完全一样，
而且是真 MTK 的文件布局、调频驱动名（mt-cpufreq）、频率表结构。只改三样：
  1) 8 个核的 midr_el1：小核 A55(0x…412fd050) → A510(0x…411fd461)、
     大核 A78(0x…411fd410) → A715(0x…411fd4d0)。midr 用 pixel-8 真机采集到的 A510/A715 值。
  2) 大核峰值频率 2.6GHz → 2.8GHz（天玑 7200 A715 标称值）。小核已是 2.0GHz，正好，不动。
  3) 元数据 name/soc/source。
其余（缓存、拓扑、OPP 表结构、idle、调频器列表）保持真 MT6877 的值——比凭空捏造更真、且内部自洽。

残留局限（和已上线的其它 ARMv9 机型同一档，改不了）：① 板子是 ARMv8.2，跑 ARMv9 专属指令会崩，
深度检测仍能看出；② 频率 OPP 表下半截仍是 MT6877 的、不是真天玑 7200 的表。合成只修「核型号和
SoC 对不上」这个浅层破绽，让这俩机型和 pixel-8 / S921B 拉平。

用法：  python3 make-mt6886-tree.py <骨架树 redmi-22101316ucp.json> <输出 mt6886.json>
"""
import json
import sys

# pixel-8 真机采集到的 midr（variant/revision 都是真实值）
A510 = "0x00000000411fd461\n"   # Cortex-A510 (part 0xd46)  ← 小核，替换 A55 0x412fd050
A715 = "0x00000000411fd4d0\n"   # Cortex-A715 (part 0xd4d)  ← 大核，替换 A78 0x411fd410
OLD_LITTLE = "412fd050"         # MT6877 的 A55
OLD_BIG = "411fd410"            # MT6877 的 A78
BIG_PEAK_OLD = "2600000"        # MT6877 大核峰值
BIG_PEAK_NEW = "2800000"        # 天玑 7200 A715 标称峰值


def midr_of(files, i):
    return files.get(f"cpu{i}/regs/identification/midr_el1", "").strip()


def main():
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    donor, out = sys.argv[1], sys.argv[2]
    t = json.load(open(donor))
    f = t["files"]

    # —— 1) 换 midr：按骨架每核原值判断大小核，换成 A510 / A715 ——
    changed = 0
    for i in range(8):
        k = f"cpu{i}/regs/identification/midr_el1"
        cur = f.get(k, "").strip()
        if OLD_LITTLE in cur:
            f[k] = A510
            changed += 1
        elif OLD_BIG in cur:
            f[k] = A715
            changed += 1
        else:
            sys.exit(f"cpu{i} 的 midr {cur!r} 不是预期的 MT6877 A55/A78，骨架树不对，停。")
    if changed != 8:
        sys.exit(f"只换了 {changed} 个核的 midr（应 8 个），停。")

    # 核对：换完应是 6 小核 A510 + 2 大核 A715（按 cpu 序号，小核在前）
    midrs = [midr_of(f, i) for i in range(8)]
    littles = sum(1 for m in midrs if m.endswith("d461"))
    bigs = sum(1 for m in midrs if m.endswith("d4d0"))
    if (littles, bigs) != (6, 2):
        sys.exit(f"换完不是 6 小 + 2 大（得到 {littles} 小 {bigs} 大）：{midrs}")

    # —— 2) 大核峰值频率 2.6 → 2.8GHz：只动 policy6（大核）频率相关文件的「最高一档」——
    #    （policy0 小核已是 2.0GHz = 天玑7200 小核标称值，不动；cpuidle residency=2600 是微秒不是频率，别碰）
    freq_keys = [
        "cpufreq/policy6/cpuinfo_max_freq",
        "cpufreq/policy6/scaling_cur_freq",
        "cpufreq/policy6/scaling_available_frequencies",
        "cpufreq/policy6/stats/time_in_state",
        "cpufreq/policy6/stats/trans_table",
    ]
    for k in freq_keys:
        if k not in f:
            continue
        # 只替换「行首/串首的最高档 2600000」，避免误伤别的数字
        v = f[k]
        if k.endswith("scaling_available_frequencies"):
            parts = v.split()
            if parts and parts[0] == BIG_PEAK_OLD:
                parts[0] = BIG_PEAK_NEW
            f[k] = " ".join(parts) + ("\n" if v.endswith("\n") else "")
        else:
            # 每一行开头的 2600000 换成 2800000（time_in_state / trans_table 首行、单值文件）
            f[k] = "\n".join(
                (BIG_PEAK_NEW + ln[len(BIG_PEAK_OLD):]) if ln.startswith(BIG_PEAK_OLD) else ln
                for ln in v.split("\n")
            )

    # —— 3) 元数据 ——
    t["name"] = "mt6886"
    t["soc"] = "MT6886"
    t["source"] = ("合成（非真机采集）：骨架取真 MT6877 树 redmi-22101316ucp（2 大核 + 6 小核，"
                   "真 MTK 文件布局/mt-cpufreq/频率表），midr 换成 pixel-8 采集的 A510(0x…d461)/"
                   "A715(0x…d4d0)、大核峰值改 2.8GHz。天玑 7200（2×A715 + 6×A510）。见 make-mt6886-tree.py。")

    json.dump(t, open(out, "w"), ensure_ascii=False, indent=0)
    print(f"✅ 写出 {out}")
    print(f"   核: {' '.join(m[-4:] for m in midrs)}（d461=A510 小核 / d4d0=A715 大核）")
    print(f"   大核峰值: {f.get('cpufreq/policy6/cpuinfo_max_freq','?').strip()}  "
          f"小核峰值: {f.get('cpufreq/policy0/cpuinfo_max_freq','?').strip()}")


if __name__ == "__main__":
    main()
