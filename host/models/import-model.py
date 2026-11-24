#!/usr/bin/env python3
"""导入机型包 → 机型档案（加进本地机型库，管家重启后就能选；和魔云腾「导入本地机型」一个意思）。

支持两种包：
  - 我们自己的采集包（host/models/collect-model.py 录的 zip）
  - 魔云腾机型包（外层 zip 里套一个机型 zip + cfg.json；新旧几种格式都认）

用法（板子上，root）：
  python3 -I host/models/import-model.py <包.zip> [<包.zip> ...] [--out /mmc/pp-data/models] [--tac <tac_by_model.json>]
           [--host-gpu Mali-G610] [--host-cpus 8] [--dry-run]
产物：<out>/models/<id>.json（机型档案）、<out>/cpu/<id>.json（真机 CPU 目录模板，有采到才有）

分档（tier）：
  consistent  真机显卡就是板子的显卡（Mali-G610）：显卡、编解码「干活」一面天然一致
  disguised   显卡不一样：只能靠伪装，查 Vulkan / 性能 / 渲染指纹会露馅（面板会提示）
CPU 和板子不是同一代（ARMv9 核心）的，标 cpuMismatch。
"""
import base64, io, json, os, re, sys, zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
KEEP = [  # 和 host/extract-buildprops.py 一样：只留身份字段
    re.compile(r"^ro\.((system|vendor|odm|product|system_ext|vendor_dlkm|odm_dlkm|system_dlkm|bootimage)\.)?build\.(fingerprint|id|tags|type|version\.incremental|date\.utc)$"),
    re.compile(r"^ro\.product\.((system|vendor|odm|product|system_ext|vendor_dlkm|odm_dlkm|system_dlkm)\.)?(brand|device|manufacturer|model|name|marketname)$"),
    re.compile(r"^ro\.build\.(display\.id|flavor|product|host|user|description)$"),
]
# ARMv9 的核（RK3588 是 A76 / A55，指令集对不上）：A510 0xd46、A710 0xd47、A715 0xd4d、A720 0xd81、A520 0xd80、X2 0xd48、X3 0xd4e、X4 0xd82
ARMV9_PARTS = {0xd46, 0xd47, 0xd4d, 0xd81, 0xd80, 0xd48, 0xd4e, 0xd82, 0xd87, 0xd85}
PART_NAMES = {0xd05: "Cortex-A55", 0xd0b: "Cortex-A76", 0xd0d: "Cortex-A77", 0xd41: "Cortex-A78", 0xd44: "Cortex-X1", 0xd46: "Cortex-A510",
              0xd47: "Cortex-A710", 0xd48: "Cortex-X2", 0xd4d: "Cortex-A715", 0xd4e: "Cortex-X3", 0xd80: "Cortex-A520", 0xd81: "Cortex-A720", 0xd82: "Cortex-X4"}
LINK_NAMES = {"cpufreq", "of_node", "subsystem", "driver", "firmware_node", "device", "port"}
SYS_CPU = "/sys/devices/system/cpu"


def parse_props(text):
    props = {}
    for line in text.splitlines():
        line = line.strip()
        m = re.match(r"^\[(.+?)\]: \[(.*)\]$", line)            # getprop 格式
        if m:
            props[m.group(1)] = m.group(2); continue
        if "=" in line and not line.startswith("#"):
            k, v = line.split("=", 1); props[k.strip()] = v.strip()
    return props


class Pkg:
    """把包里的文件读成 {相对路径: bytes}，链接 {相对路径: 目标}（魔云腾的包剥掉外层和机型目录前缀）。"""
    def __init__(self, path):
        self.files, self.links, self.kind, self.cfg = {}, {}, "", {}
        if os.path.isdir(path):                       # 已经解开的目录（例如板子上 /mmc/phoneModel/<包>/<型号>）
            self.kind = "ours" if os.path.exists(os.path.join(path, "meta.json")) else "mytos"
            cfg = os.path.join(os.path.dirname(path.rstrip("/")), "cfg.json")
            if os.path.exists(cfg):
                self.cfg = json.load(open(cfg))
            for root, dirs, files in os.walk(path):
                for n in dirs + files:
                    full = os.path.join(root, n); rel = os.path.relpath(full, path).replace(os.sep, "/")
                    if os.path.islink(full):
                        self.links[rel] = os.readlink(full)
                    elif os.path.isfile(full):
                        self.files[rel] = open(full, "rb").read()
            return
        z = zipfile.ZipFile(path)
        names = z.namelist()
        inner = [n for n in names if n.endswith(".zip")]
        if "meta.json" in names:
            self.kind = "ours"; self._load(z, "")
        elif inner:
            self.kind = "mytos"
            if "cfg.json" in names:
                self.cfg = json.loads(z.read("cfg.json"))
            iz = zipfile.ZipFile(io.BytesIO(z.read(inner[0])))
            # 有的包里面还有一层机型同名目录（PJA110/…）
            tops = {n.split("/")[0] for n in iz.namelist()}
            prefix = ""
            if len(tops) == 1 and not any(n == next(iter(tops)) for n in iz.namelist()):
                prefix = next(iter(tops)) + "/"
            self._load(iz, prefix)
        else:
            raise SystemExit(f"{path}：认不出是什么机型包")

    def _load(self, z, prefix):
        for info in z.infolist():
            n = info.filename
            if not n.startswith(prefix) or n.endswith("/"):
                continue
            rel = n[len(prefix):]
            data = z.read(info)
            if (info.external_attr >> 16) & 0o170000 == 0o120000:
                self.links[rel] = data.decode(); continue
            self.files[rel] = data

    def text(self, name):
        b = self.files.get(name)
        return b.decode("utf-8", "replace") if b is not None else ""


def cpu_tree(pkg):
    files, links, dirs = {}, {}, set()
    for rel, data in pkg.files.items():
        if not rel.startswith("cpu/"):
            continue
        p = rel[4:]
        try:
            s = data.decode("utf-8")
        except UnicodeDecodeError:
            continue
        base = p.split("/")[-1]
        st = s.strip()
        if base in LINK_NAMES and st and "\n" not in st and (st.startswith("../") or st.startswith("/")):
            parent = SYS_CPU + ("/" + os.path.dirname(p) if os.path.dirname(p) else "")
            target = os.path.normpath(os.path.join(parent, st))
            links[p] = os.path.relpath(target, parent) if target == SYS_CPU or target.startswith(SYS_CPU + "/") else st
            continue
        files[p] = s
    for rel, tgt in pkg.links.items():
        if rel.startswith("cpu/"):
            links[rel[4:]] = tgt
    if not any(re.match(r"^cpu\d+/regs/identification/midr_el1$", p) for p in files):
        return None
    return {"files": files, "links": links, "dirs": sorted(dirs)}


def cpu_parts_from(tree, cpuinfo):
    midrs = []
    if tree:
        i = 0
        while f"cpu{i}/regs/identification/midr_el1" in tree["files"]:
            midrs.append(int(tree["files"][f"cpu{i}/regs/identification/midr_el1"].strip(), 16)); i += 1
    if not midrs:   # 没有 CPU 目录：从 cpuinfo 拼
        cur = {}
        for line in cpuinfo.splitlines() + [""]:
            if not line.strip():
                if "CPU part" in cur:
                    midrs.append((int(cur.get("CPU implementer", "0x41"), 16) << 24) | (int(cur.get("CPU variant", "0"), 16) << 20)
                                 | (0xf << 16) | (int(cur["CPU part"], 16) << 4) | int(cur.get("CPU revision", "0")))
                cur = {}; continue
            k, _, v = line.partition(":"); cur[k.strip()] = v.strip()
    parts = []
    for m in midrs:
        part = (m >> 4) & 0xfff
        s = f"0x{m:08x}"
        if parts and parts[-1]["midr"] == s:
            parts[-1]["count"] += 1
        else:
            parts.append({"part": PART_NAMES.get(part, f"0x{part:03x}"), "midr": s, "count": 1})
    return parts, midrs


def max_freqs(tree, n):
    out = []
    for i in range(n):
        v = (tree or {}).get("files", {}).get(f"cpu{i}/cpufreq/cpuinfo_max_freq", "")
        if not v:
            link = (tree or {}).get("links", {}).get(f"cpu{i}/cpufreq", "")
            if link:
                v = tree["files"].get(os.path.normpath(os.path.join(f"cpu{i}", link, "cpuinfo_max_freq")), "")
        if v.strip().isdigit():
            out.append(int(v.strip()))
    return out if len(out) == n else None


def gpu_of(pkg, props):
    di = pkg.text("device_info.json")
    m = re.search(r'"(?:gles20\.renderer|glRenderer)"\s*:\s*"([^"]+)"', di)
    if m:
        return m.group(1)
    m = re.search(r"GLES: [^,]+, ([^,]+),", pkg.text("gpu/surfaceflinger.txt"))
    if m:
        return m.group(1).strip()
    m = re.search(r"(Mali-[A-Z0-9 -]+|Adreno \(TM\) \d+|Xclipse \d+)", pkg.text("adb_debug.prop") + pkg.text("props.txt"))
    return m.group(1).strip() if m else ""


def gpu_info(pkg, props):
    """真机的显卡字符串（伪装档用：转接库照这个回答 glGetString）。没录的返回 None。"""
    info, egl_hw = {}, ""
    di = pkg.text("device_info.json")
    if di:
        try:
            g = json.loads(di).get("gpu") or {}
        except ValueError:
            g = {}
        egl_hw = g.get("eglHardware", "")
        # 两种写法：gles20.renderer……（带扩展列表）；glRenderer……（老一点的包，只有扩展数量，没有列表）
        info = {"vendor": g.get("gles20.vendor") or g.get("glVendor", ""), "renderer": g.get("gles20.renderer") or g.get("glRenderer", ""),
                "version": g.get("gles20.version") or g.get("glVersion", ""), "extensions": g.get("gles20.extensions") or g.get("glExtensions", "")}
    if not info.get("renderer"):
        # 我们采集的：dumpsys SurfaceFlinger 的「GLES: 厂商, 显卡名, 版本」，下一行是扩展列表
        sf = pkg.text("gpu/surfaceflinger.txt").splitlines()
        for i, l in enumerate(sf):
            m = re.match(r"\s*GLES: ([^,]+), ([^,]+), (.+)$", l)
            if m:
                info = {"vendor": m.group(1).strip(), "renderer": m.group(2).strip(), "version": m.group(3).strip(),
                        "extensions": sf[i + 1].strip() if i + 1 < len(sf) and sf[i + 1].strip().startswith("GL_") else ""}
                break
    if not info.get("renderer"):
        return None
    m = re.match(r"OpenGL ES (\d)\.(\d)", info.get("version", ""))
    info["glsl"] = f"OpenGL ES GLSL ES {m.group(1)}.{m.group(2)}0" if m and m.group(1) >= "3" else ("OpenGL ES GLSL ES 1.00" if m else "")
    info["egl"] = props.get("ro.hardware.egl", "") or egl_hw
    info["vulkan"] = props.get("ro.hardware.vulkan", "")
    return {k: v for k, v in info.items() if v}


def display_of(pkg):
    d = pkg.text("display.json")
    if d:
        try:
            j = json.loads(d)
            mode = (j.get("displayModes") or [{}])[0]
            dens = re.search(r'"(?:densityDpi|logicalDensityDpi|dpi)"\s*:\s*(\d+)', d)
            return {"w": int(mode.get("physicalWidth", 0)), "h": int(mode.get("physicalHeight", 0)),
                    "refresh": round(float(mode.get("refreshRate", 60))), "dpi": int(dens.group(1)) if dens else None}
        except Exception:
            pass
    t = pkg.text("display.txt")
    m, dm = re.search(r"Physical size: (\d+)x(\d+)", t), re.search(r"Physical density: (\d+)", t)
    rr = re.findall(r"fps=([\d.]+)", t)
    if m:
        return {"w": int(m.group(1)), "h": int(m.group(2)), "dpi": int(dm.group(1)) if dm else None,
                "refresh": round(max(float(x) for x in rr)) if rr else 60}
    return {}


def build_props_from(pkg, props):
    # 我们的采集包有各分区 build.prop 原文件：用 extract-buildprops.py 的规则（按加载顺序合并）
    bp = {k: v for k, v in pkg.files.items() if k.startswith("buildprop/")}
    if bp:
        import importlib.util, tempfile
        eb_path = next(p for p in (os.path.join(HERE, "extract-buildprops.py"), os.path.join(os.path.dirname(HERE), "extract-buildprops.py")) if os.path.exists(p))
        spec = importlib.util.spec_from_file_location("eb", eb_path)
        eb = importlib.util.module_from_spec(spec); spec.loader.exec_module(eb)
        with tempfile.TemporaryDirectory() as d:
            for k, v in bp.items():
                open(os.path.join(d, k[len("buildprop/"):]), "wb").write(v)
            out, _ = eb.extract(d, sku=props.get("ro.boot.hardware.sku") or None)
            return out
    return {k: v for k, v in sorted(props.items()) if v and any(r.match(k) for r in KEEP)}


def mem_gb(meminfo):
    m = re.search(r"MemTotal:\s+(\d+)", meminfo)
    if not m:
        return None
    gb = int(m.group(1)) / 1024 / 1024
    for std in (2, 3, 4, 6, 8, 12, 16, 18, 24):
        if gb <= std:
            return std
    return round(gb)


def slug(s):
    return re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-")


def convert(path, tac_db, host_gpu, host_cpus):
    pkg = Pkg(path)
    props = parse_props(pkg.text("props.txt") or pkg.text("adb_debug.prop"))
    di = pkg.text("device_info.json")
    for k in ("ro.product.brand", "ro.product.model", "ro.product.manufacturer", "ro.product.device", "ro.build.fingerprint",
              "ro.build.version.release", "ro.soc.model", "ro.soc.manufacturer", "ro.board.platform", "ro.product.marketname"):
        if not props.get(k):
            m = re.search(r'"' + re.escape(k) + r'"\s*:\s*"([^"]*)"', di)
            if m:
                props[k] = m.group(1).replace("\\/", "/")
    rel = props.get("ro.build.version.release") or str(pkg.cfg.get("android_version", ""))
    model, brand = props.get("ro.product.model", ""), props.get("ro.product.brand", "")
    if not (model and brand and rel):
        raise SystemExit(f"{path}：缺型号 / 品牌 / 安卓版本，不像完整的机型包")
    gpu = gpu_of(pkg, props)
    family = re.sub(r"\s+M[CP]\d+$", "", gpu).replace(" (TM)", "")
    tree = cpu_tree(pkg)
    parts, midrs = cpu_parts_from(tree, pkg.text("cpuinfo") or pkg.text("proc/cpuinfo"))
    disp = display_of(pkg)
    mid = slug(f"{brand}-{model}")
    v9 = sorted({PART_NAMES.get((m >> 4) & 0xfff, hex((m >> 4) & 0xfff)) for m in midrs if (m >> 4) & 0xfff in ARMV9_PARTS})
    tok = re.compile(r"(^|\s)" + re.escape(model.upper()) + r"($|\s)")   # TAC 库的键是「品牌 机型名 型号代码」
    tac_key = next((k for k in tac_db if tok.search(k.upper())), None)
    values = {k: v for k, v in {
        "brand": brand, "manufacturer": props.get("ro.product.manufacturer") or brand, "model": model,
        "marketName": props.get("ro.product.marketname") or props.get("ro.product.vendor.marketname") or model,
        "device": props.get("ro.product.device"), "socModel": props.get("ro.soc.model"), "socManufacturer": props.get("ro.soc.manufacturer"),
        "cpuParts": parts or None, "cpuMaxFreq": max_freqs(tree, len(midrs)) if midrs else None,
        "memTotal": mem_gb(pkg.text("meminfo") or pkg.text("proc/meminfo")),
        "resolution": {"w": disp["w"], "h": disp["h"]} if disp.get("w") else None, "dpi": disp.get("dpi"),
        "refreshRate": disp.get("refresh"), "touchPoints": 10, "boardPlatform": props.get("ro.board.platform"),
    }.items() if v not in (None, "", [])}
    prof = {
        "id": mid, "schema": 1, "verified": False,
        "verifyNote": f"导入自{'我们采集的' if pkg.kind == 'ours' else '魔云腾'}机型包 {os.path.basename(path)}（真机安卓 {rel} 数据）；显卡 {gpu or '未知'}",
        "gpuFamily": family or "unknown", "market": "CN" if re.search(r"CN|cn_|_cn|china", props.get("ro.build.fingerprint", "") + props.get("ro.product.locale", "") + props.get("ro.product.mod_device", "")) else "GLOBAL",
        "tier": "consistent" if family == host_gpu else "disguised",
        "local": True, "source": pkg.kind,
        "values": values, "androidVersions": [int(rel)] if rel.isdigit() else [],
    }
    gi = gpu_info(pkg, props)
    if prof["tier"] == "disguised" and not gi:
        raise SystemExit(f"{path}：伪装档要真机的显卡字符串（显卡名 / 版本 / 扩展列表），这个包没录（魔云腾旧格式包），不导入")
    if gi:
        prof["gpu"] = gi
    if props.get("ro.hardware"):
        prof["hardware"] = props["ro.hardware"]
    if tac_key:
        prof["imeiTacs"] = tac_db[tac_key][:8]
    if v9:
        prof["cpuMismatch"] = f"CPU 是 {'/'.join(v9)}（ARMv9），板子 RK3588 是上一代的 A76 / A55：指令集伪装不了，深度检测会看出来"
    if props.get("ro.build.fingerprint"):
        prof["fingerprints"] = {rel: props["ro.build.fingerprint"]}
    bp = build_props_from(pkg, props)
    if bp:
        prof["buildProps"] = {rel: bp}
    cpu = None
    if tree and len(midrs) == host_cpus:
        cpu = {"name": mid, "soc": props.get("ro.soc.model", ""), "source": prof["verifyNote"], **tree}
        prof["cpuTree"] = mid
    return prof, cpu


def main():
    import argparse
    ap = argparse.ArgumentParser(description="导入机型包到本地机型库")
    ap.add_argument("pkgs", nargs="+", help="机型包 zip，或已经解开的目录")
    ap.add_argument("--out", default="/mmc/pp-data/models")
    ap.add_argument("--tac", default="", help="TAC 库（型号 → IMEI 前 8 位），默认找 <out>/../tac_by_model.json、/mmc/mytTemp/resource/imei/tac_by_model.json")
    ap.add_argument("--host-gpu", default="Mali-G610")
    ap.add_argument("--host-cpus", type=int, default=os.cpu_count() or 8)
    ap.add_argument("--dry-run", action="store_true")
    a = ap.parse_args()
    tp = a.tac or next((p for p in (os.path.join(os.path.dirname(a.out.rstrip("/")), "tac_by_model.json"), "/mmc/mytTemp/resource/imei/tac_by_model.json") if os.path.exists(p)), "")
    tac = json.load(open(tp)) if tp else {}
    for p in a.pkgs:
        try:
            prof, cpu = convert(p, tac, a.host_gpu, a.host_cpus)
        except (SystemExit, zipfile.BadZipFile, KeyError, ValueError) as e:
            print(f"✗ {os.path.basename(p.rstrip('/'))}：{e}"); continue
        bp = next(iter(prof.get("buildProps", {}).values()), {})
        print(f"{'✓' if prof['tier'] == 'consistent' else '·'} {prof['id']:<26} {prof['values'].get('marketName', '')[:22]:<22} 安卓{prof['androidVersions']}"
              f" {prof['gpuFamily']:<22} {prof['tier']:<10} CPU目录={'有' if cpu else '无'} TAC={len(prof.get('imeiTacs', []))} 分区属性={len(bp)}"
              + (" ⚠CPU不同代" if prof.get("cpuMismatch") else ""))
        if a.dry_run:
            continue
        os.makedirs(os.path.join(a.out, "models"), exist_ok=True)
        with open(os.path.join(a.out, "models", prof["id"] + ".json"), "w", encoding="utf-8") as fh:
            json.dump(prof, fh, ensure_ascii=False, indent=2); fh.write("\n")
        if cpu:
            os.makedirs(os.path.join(a.out, "cpu"), exist_ok=True)
            with open(os.path.join(a.out, "cpu", prof["id"] + ".json"), "w", encoding="utf-8") as fh:
                json.dump(cpu, fh, ensure_ascii=False, sort_keys=True); fh.write("\n")


if __name__ == "__main__":
    main()
