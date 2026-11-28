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
# 个别机型的采集包 densityDpi 不可信（采集时开了「放大显示」，如三星 S24 录成 600=288dp，真机出厂是 450=384dp）：用出厂默认兜底
DPI_FALLBACK = {"samsung-sm-s921b": 450}


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
        self.files, self.links, self.kind, self.cfg, self.model_json = {}, {}, "", {}, None
        # 兜底导入（冗余）：不是 zip 包、而是单个文件时，尽量认出来；缺的字段后面 convert 按板子默认补。
        ext = path.lower().rsplit(".", 1)[-1] if ("." in os.path.basename(path) and os.path.isfile(path)) else ""
        if ext == "json":
            raw = open(path, "rb").read()
            try:
                j = json.loads(raw.decode("utf-8", "replace"))
            except Exception:
                j = None
            # 已经是我们的机型档（有 values / gpuFamily）→ 直接存，不用再解析
            if isinstance(j, dict) and ("values" in j or "gpuFamily" in j):
                self.kind = "model-json"; self.model_json = j; return
            # 否则当成魔云腾 device_info.json（convert 大量读它）
            self.kind = "loose"; self.files["device_info.json"] = raw; return
        if ext in ("prop", "properties", "txt", "getprop", "log"):   # build.prop / getprop 转储
            self.kind = "loose"; self.files["props.txt"] = open(path, "rb").read(); return
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
            links[rel[4:]] = normalize_cpu_link(rel[4:], tgt)
    if not any(re.match(r"^cpu\d+/regs/identification/midr_el1$", p) for p in files):
        return None
    return {"files": files, "links": links, "dirs": sorted(dirs)}


def normalize_cpu_link(rel, target):
    """把采集到的符号链接目标还原成真机 sysfs 的样子。
    有的魔云腾包把 cpuN/cpufreq、of_node、subsystem 等存成**真符号链接**，目标是解包时的临时
    提取路径（/data/app/.../task_xxx/...）——照搬进树会让 App `readlink` 到提取路径，大破绽。
    这里按锚点把它还原：cpufreq 指回本树的 policy（和现有树一样的相对写法），其余还原到 /sys。"""
    t = (target or "").replace("\\", "/").strip()
    base = rel.split("/")[-1]
    if not t or t.startswith("../") or t.startswith("/sys/"):
        return t                                   # 已经是规整的
    if base == "cpufreq":
        m = re.search(r"/cpufreq/(policy\d+)", t)
        if m:
            return "..//cpu/cpufreq/" + m.group(1)  # 对齐现有树（honor 等）的写法
    for anchor, pre in (("/firmware/", "/sys"), ("/platform/", "/sys/devices"),
                        ("/bus/", "/sys"), ("/devices/", "/sys")):
        i = t.find(anchor)
        if i >= 0:
            return pre + t[i:]
    return t


def fit_tree_to_host(tree, host):
    """采集核数 > 板子核数（host）时，把 CPU 目录模板缩到 host 个核、重编号成 0..host-1（踩坑 #147）。
    魔云腾内核要求模板核数 == 板子物理核数，否则把 /sys/.../cpu 下路径按目录接管、整棵树挂不上
    （这正是 Tensor G3 / Exynos 这类 9、10 核机型之前没有 CPU 树、midr 伪装不了的根因）。
    缩核规则：只要核数 > host，就从「保留核最多的频率组」（并列取最高频最低的）里删掉编号最大的核，
    每个频率组至少留 1 个（保住大/中/小核结构）；然后把所有核号、policy 名、CPU 列表 / 位掩码、
    cpufreq 链接目标一并改好。只做缩核（host 以内原样返回）。"""
    files, links, dirs = tree["files"], tree["links"], tree.get("dirs", [])
    cpus = sorted({int(m.group(1)) for p in files for m in [re.match(r"cpu(\d+)(?:/|$)", p)] if m})
    if len(cpus) <= host:
        return tree
    policies = {}                                   # policy 名 -> 该组的核集合
    for p, v in files.items():
        m = re.match(r"cpufreq/(policy\d+)/related_cpus$", p)
        if m:
            policies[m.group(1)] = {int(x) for x in v.split()}
    if not policies:
        policies = {"policy0": set(cpus)}            # 没频率组信息：整组当一簇兜底
    pol_max = {}
    for pol in policies:
        mf = files.get(f"cpufreq/{pol}/cpuinfo_max_freq", "").strip()
        pol_max[pol] = int(mf) if mf.isdigit() else 0
    pol_of = {c: pol for pol, cs in policies.items() for c in cs}
    keep = set(cpus)
    while len(keep) > host:
        groups = {}
        for c in keep:
            groups.setdefault(pol_of.get(c), []).append(c)
        cand = [(pol, cs) for pol, cs in groups.items() if pol is not None and len(cs) > 1]
        if not cand:
            break
        cand.sort(key=lambda kv: (-len(kv[1]), pol_max.get(kv[0], 0)))
        keep.discard(max(cand[0][1]))               # 删这组里编号最大的核
    keep = sorted(keep)
    remap = {old: new for new, old in enumerate(keep)}       # 旧核号 -> 新核号（连续 0..host-1）
    pol_rename = {}                                          # 旧 policy 名 -> 新名（按组内最小新核号）
    for pol, cs in policies.items():
        kept = sorted(remap[c] for c in cs if c in remap)
        if kept:
            pol_rename[pol] = f"policy{kept[0]}"

    SENT = "\x00"

    def ren_path(p):
        p2 = re.sub(r"^cpu(\d+)(?=/|$)",
                    lambda m: f"cpu{remap[int(m.group(1))]}" if int(m.group(1)) in remap else SENT, p)
        if SENT in p2:
            return None                             # 被删核的条目：丢掉
        return re.sub(r"cpufreq/(policy\d+)",
                      lambda m: "cpufreq/" + pol_rename.get(m.group(1), m.group(1)), p2)

    def ren_list(s):
        spaced = " " in s.strip()
        got = []
        for tok in s.replace(",", " ").split():
            if "-" in tok:
                a, b = tok.split("-"); rng = range(int(a), int(b) + 1)
            else:
                rng = [int(tok)]
            got += [remap[c] for c in rng if c in remap]
        got = sorted(set(got))
        if not got:
            return ""
        if spaced:
            return " ".join(str(c) for c in got)
        out, i = [], 0
        while i < len(got):
            j = i
            while j + 1 < len(got) and got[j + 1] == got[j] + 1:
                j += 1
            out.append(str(got[i]) if i == j else f"{got[i]}-{got[j]}")
            i = j + 1
        return ",".join(out)

    def ren_mask(s):
        groups = s.split(",")
        if not all(re.fullmatch(r"[0-9a-fA-F]+", g.strip() or "") for g in groups):
            return s
        bits = 0
        for g in groups:
            bits = (bits << 32) | int(g.strip(), 16)
        nb = 0
        for old, new in remap.items():
            if (bits >> old) & 1:
                nb |= 1 << new
        if len(groups) > 1:
            hx = f"{nb:0{len(groups) * 8}x}"[-len(groups) * 8:]
            return ",".join(hx[i:i + 8] for i in range(0, len(hx), 8))
        return f"{nb:0{max(1, (host + 3) // 4)}x}"   # 位宽按板子核数（8 核 -> 2 位，和真机 8 核树一致）

    LIST_BASES = {"online", "present", "possible", "offline", "isolated", "related_cpus", "affected_cpus"}
    MASK_BASES = {"package_cpus", "core_cpus", "core_siblings", "thread_siblings", "die_cpus", "cluster_cpus"}

    def nl_suffix(v):
        return "\n" if v.endswith("\n") else ""

    nf = {}
    for p, v in files.items():
        np_ = ren_path(p)
        if np_ is None:
            continue
        base = p.split("/")[-1]
        if base in LIST_BASES or base.endswith("_list"):
            v = ren_list(v.strip()) + nl_suffix(v)
        elif base in MASK_BASES or base.endswith("_map"):
            v = ren_mask(v.strip()) + nl_suffix(v)
        nf[np_] = v
    nl = {}
    for p, tgt in links.items():
        np_ = ren_path(p)
        if np_ is None:
            continue
        base = p.split("/")[-1]
        if base == "cpufreq":
            m = re.search(r"(policy\d+)", tgt)
            if m:
                tgt = "..//cpu/cpufreq/" + pol_rename.get(m.group(1), m.group(1))
        elif base == "of_node":
            mi = re.match(r"cpu(\d+)/of_node$", np_)
            if mi:
                tgt = f"/sys/firmware/devicetree/base/cpus/cpu@{int(mi.group(1)) * 0x100:x}"
        nl[np_] = tgt
    nd = sorted(x for x in (ren_path(d) for d in dirs) if x is not None)
    tree["files"], tree["links"], tree["dirs"] = nf, nl, nd
    return tree


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
    out = {}
    d = pkg.text("display.json")
    if d:
        try:
            j = json.loads(d)
            mode = (j.get("displayModes") or [{}])[0]
            # densityDpi 优先在 display.json 找，没有再从 device_info.json 兜底（有的包 dpi 只在那里，如三星 S24）
            dens = re.search(r'"(?:densityDpi|logicalDensityDpi|dpi)"\s*:\s*(\d+)', d) \
                or re.search(r'(?:densityDpi|logicalDensityDpi)[\\"\s]*:\s*(\d+)', pkg.text("device_info.json") or "")
            out = {"w": int(mode.get("physicalWidth", 0)), "h": int(mode.get("physicalHeight", 0)),
                   "refresh": round(float(mode.get("refreshRate", 60))), "dpi": int(dens.group(1)) if dens else None}
        except Exception:
            out = {}
    if not out:
        t = pkg.text("display.txt")
        m, dm = re.search(r"Physical size: (\d+)x(\d+)", t), re.search(r"Physical density: (\d+)", t)
        rr = re.findall(r"fps=([\d.]+)", t)
        if m:
            out = {"w": int(m.group(1)), "h": int(m.group(2)), "dpi": int(dm.group(1)) if dm else None,
                   "refresh": round(max(float(x) for x in rr)) if rr else 60}
    # dpi 合理性护栏：真机出厂逻辑宽度都在 320~440dp，超出多半是采集时开了「放大显示」，这个 dpi 不可信，丢弃
    if out.get("dpi") and out.get("w"):
        dpw = out["w"] / out["dpi"] * 160
        if not (320 <= dpw <= 440):
            out["dpi"] = None
    return out


BUILD_MAP = {  # 魔云腾 device_info.json 的 prop.Build.* / VERSION.* → ro.* 属性（兜底单文件导入用）
    "Build.MODEL": "ro.product.model", "Build.BRAND": "ro.product.brand", "Build.MANUFACTURER": "ro.product.manufacturer",
    "Build.DEVICE": "ro.product.device", "Build.PRODUCT": "ro.product.name", "Build.FINGERPRINT": "ro.build.fingerprint",
    "Build.HARDWARE": "ro.hardware", "Build.BOARD": "ro.board.platform", "Build.ID": "ro.build.id",
    "Build.TYPE": "ro.build.type", "Build.TAGS": "ro.build.tags", "Build.DISPLAY": "ro.build.display.id",
    "Build.MARKETNAME": "ro.product.marketname", "Build.ODM_MARKETNAME": "ro.product.marketname",
    "VERSION.RELEASE": "ro.build.version.release", "VERSION.INCREMENTAL": "ro.build.version.incremental",
    "VERSION.SECURITY_PATCH": "ro.build.version.security_patch",
}


def di_build_props(pkg):
    """从 device_info.json 的 prop.Build.* 抽出 ro.* 属性（兜底单文件导入用，zip 包里有 props.txt 就用不着）。"""
    b = (device_info(pkg).get("prop") or {}).get("Build") or {}
    out = {}
    for k, ro in BUILD_MAP.items():
        v = b.get(k)
        if isinstance(v, str) and v and v != "unknown":
            out[ro] = v.replace("\\/", "/")
    return out


def device_info(pkg):
    """魔云腾新格式的 device_info.json（解析不了返回 {}）。"""
    try:
        j = json.loads(pkg.text("device_info.json") or "{}")
        return j if isinstance(j, dict) else {}
    except ValueError:
        return {}


def kernel_of(pkg):
    """真机内核版本号 + 编译信息（uname -r / uname -v）：我们的采集包读 proc/version；魔云腾包读 kernel.json 或
    device_info.json 里的 kernel.dg_config（uname.version 结尾多一个 aarch64，是它的采集工具把 uname -a 切错了）。"""
    m = re.match(r"Linux version (\S+) .*?(#\d+ .+?)\s*$", pkg.text("proc/version").strip(), re.S)
    if m:
        return m.group(1), m.group(2)
    dg = ((device_info(pkg).get("kernel") or {}).get("dg_config") or {})
    rel = dg.get("kernel.uname.release") or ""
    if not rel:
        try:
            rel = json.loads(pkg.text("kernel.json") or "{}").get("kernelVersion", "")
        except ValueError:
            rel = ""
    build = re.sub(r"\s+(aarch64|armv8l|Toybox)$", "", (dg.get("kernel.uname.version") or "").strip())
    if not rel:
        m = re.search(r'"uname"\s*:\s*"Linux \S+ (\S+) (#.*?)\s+aarch64', pkg.text("device_info.json"))
        if m:
            rel, build = m.group(1), build or m.group(2)
    return rel.strip(), (build if build.startswith("#") else "")


STORAGE_SIZES = (16, 32, 64, 128, 256, 512, 1024, 2048)


def storage_of(pkg):
    """/data 的真实总字节数（真机 256GB 的 /data 大约 241GB：系统分区占了一块）和标称容量（GB）。"""
    total = 0
    try:
        total = int((json.loads(pkg.text("storage.json") or "{}").get("internal") or {}).get("totalSpace") or 0)
    except (ValueError, AttributeError):
        pass
    di = device_info(pkg)
    total = total or int(((di.get("storage") or {}).get("internal") or {}).get("totalBytes") or 0)
    total = total or int(((di.get("kernel") or {}).get("dg_config") or {}).get("kernel.disk.data.total") or 0)
    if not total:                                   # 我们的采集包：df -k /data
        m = re.search(r"^\S+\s+(\d+)\s+\d+\s+\d+\s+\d+%\s+/data\s*$", pkg.text("storage.txt"), re.M)
        total = int(m.group(1)) * 1024 if m else 0
    if total < 8e9:
        return 0, 0
    return total, next((g for g in STORAGE_SIZES if g * 1e9 >= total), 0)


def features_of(pkg):
    """系统特性列表（pm list features），每项 "名字" 或 "名字=版本"。"""
    out = []
    for line in pkg.text("features.txt").splitlines():
        line = line.strip()
        if line.startswith("feature:") and not line.startswith("feature:reqGlEsVersion"):
            out.append(line[len("feature:"):])
    if out:
        return out
    feats = []
    try:
        j = json.loads(pkg.text("features.json") or "{}")
        feats = j.get("features") or []
    except ValueError:
        pass
    feats = feats or ((device_info(pkg).get("pm") or {}).get("features") or [])
    for f in feats:
        n = (f or {}).get("name")
        if n:
            v = f.get("version") or 0
            out.append(f"{n}={v}" if v else n)
    return out


def sensors_of(pkg):
    """真机传感器清单：[{type, name, vendor, version, maxRange, resolution, power, minDelay, maxDelay, fifoRes, fifoMax, flags}]。
    魔云腾包 sensor.json 有全套；我们的采集包是 dumpsys sensorservice 文本（没有量程 / 精度 / 功耗，留 0 = 用 HAL 自己的）。"""
    out = []
    try:
        j = json.loads(pkg.text("sensor.json") or "{}")
    except ValueError:
        j = {}
    lst = j.get("sensor_list") if isinstance(j, dict) else None
    if not lst:
        lst = ((device_info(pkg).get("sensor") or {}).get("sensorList") or (device_info(pkg).get("sensor") or {}).get("sensor_list") or [])
    for x in lst or []:
        if not isinstance(x, dict) or not x.get("name") or not x.get("type"):
            continue
        out.append({"type": int(x["type"]), "name": str(x["name"]), "vendor": str(x.get("vendor") or ""), "version": int(x.get("version") or 1),
                    "maxRange": float(x.get("maximum_range") or x.get("maxRange") or 0), "resolution": float(x.get("resolution") or 0),
                    "power": float(x.get("power") or 0), "minDelay": int(x.get("mMinDelay", x.get("minDelay", 0)) or 0),
                    "maxDelay": int(x.get("mMaxDelay", x.get("maxDelay", 0)) or 0), "fifoRes": int(x.get("mFifoReservedEventCount") or 0),
                    "fifoMax": int(x.get("mFifoMaxEventCount") or 0), "flags": int(x.get("mFlags") or 0)})
    if out:
        return out
    lines = pkg.text("sensors.txt").splitlines()
    for i, l in enumerate(lines):
        m = re.match(r"^\s*0x[0-9a-f]+\)\s*(.+?)\s*\|\s*(.*?)\s*\|\s*ver:\s*(\d+)\s*\|\s*type:\s*\S+\((\d+)\).*?flags:\s*0x([0-9a-f]+)", l)
        if not m:
            continue
        nxt = lines[i + 1] if i + 1 < len(lines) else ""
        hz = lambda k: float(re.search(k + r"=([\d.]+)Hz", nxt).group(1)) if re.search(k + r"=([\d.]+)Hz", nxt) else 0
        fifo = re.search(r"FIFO \(max,reserved\) = \((\d+), (\d+)\)", nxt)
        maxr, minr = hz("maxRate"), hz("minRate")
        out.append({"type": int(m.group(4)), "name": m.group(1), "vendor": m.group(2), "version": int(m.group(3)),
                    "maxRange": 0, "resolution": 0, "power": 0,
                    "minDelay": int(round(1e6 / maxr)) if maxr else (-1 if "one-shot" in nxt else 0),
                    "maxDelay": int(round(1e6 / minr)) if minr else 0,
                    "fifoRes": int(fifo.group(2)) if fifo else 0, "fifoMax": int(fifo.group(1)) if fifo else 0,
                    "flags": int(m.group(5), 16)})
    # dumpsys 里还有安卓自己融合出来的（厂商 AOSP、句柄 0x5f...），不是硬件传感器
    return [x for x in out if x["vendor"] != "AOSP"]


def codecs_of(pkg):
    """真机的硬件编解码器：[{name, canonical, type, encoder}]（别名的 canonical 是它指向的编解码器）。
    魔云腾包 mediaCode.json（MediaCodecList 原样）；我们的采集包是 codecs/ 下的 media_codecs*.xml。软件的（c2.android / OMX.google）不要。"""
    out = []
    try:
        lst = json.loads(pkg.text("mediaCode.json") or "[]")
    except ValueError:
        lst = []
    if isinstance(lst, dict):
        lst = lst.get("mediaList") or lst.get("codecs") or lst.get("codecList") or []
    if not lst:
        lst = ((device_info(pkg).get("codec") or {}).get("codecs") or [])
    soft = re.compile(r"^(c2\.android\.|OMX\.google\.|c2\.google\.)|ffmpeg|\.sw\.|soft", re.I)
    for x in lst if isinstance(lst, list) else []:
        if not isinstance(x, dict) or not x.get("getName"):
            continue
        name = x["getName"]
        # 老一点的格式没有「硬件 / 编码器」标志：按名字猜（只要视频的、非软件的；.secure 是加密播放专用，不要）
        hw = x.get("isHardwareAccelerated")
        types = x.get("getSupportedTypes") or []
        if hw is None:
            hw = not soft.search(name) and all(t.startswith("video/") for t in types)
        if not hw or name.endswith(".secure"):
            continue
        enc = x.get("isEncoder")
        if enc is None:
            enc = "encoder" in name.lower()
        for t in types:
            out.append({"name": name, "canonical": x.get("getCanonicalName") or name, "type": t, "encoder": bool(enc)})
    if out:
        return out
    for fname in sorted(k for k in pkg.files if k.startswith("codecs/")):
        enc = None
        for m in re.finditer(r"<(/?)(Encoders|Decoders)>|<MediaCodec\s+name=\"([^\"]+)\"(?:\s+type=\"([^\"]+)\")?[^>]*?(/?)>(.*?)(?:</MediaCodec>|(?=<MediaCodec)|$)",
                             pkg.text(fname), re.S):
            if m.group(2):
                enc = (m.group(2) == "Encoders") if not m.group(1) else None
                continue
            name = m.group(3)
            if soft.match(name):
                continue
            body = m.group(6) or ""
            types = [m.group(4)] if m.group(4) else re.findall(r"<Type\s+name=\"([^\"]+)\"", body)
            e = enc if enc is not None else ("encoder" in name.lower())
            for t in types:
                out.append({"name": name, "canonical": name, "type": t, "encoder": e})
                for a in re.findall(r"<Alias\s+name=\"([^\"]+)\"", body):
                    out.append({"name": a, "canonical": name, "type": t, "encoder": e})
    return out


def cameras_of(pkg):
    """真机摄像头：[{facing: back/front, width, height, focalLengths, apertures, orientation, physicalSize}]，每个朝向只要第一个。
    魔云腾三种写法：camera.json {"camera": [{facing 0 前 / 1 后, focalLengths, apertures, streamConfigurationMap [[宽, 高]]}]}；
    camera.json [{facing "BACK", characteristics {整套 CameraCharacteristics}}]；device_info.json 的 camera.cameras [{facing, supportedSizes}]。"""
    out = {}
    def num(v):
        try:
            return float(v)
        except (TypeError, ValueError):
            return None
    def add(facing, w, h, fl=None, ap=None, ori=None, phys=None):
        facing = {"0": "front", "1": "back", "FRONT": "front", "BACK": "back"}.get(str(facing).upper() if not str(facing).isdigit() else str(facing), str(facing).lower())
        if facing not in ("back", "front") or facing in out or not w or not h:
            return
        c = {"facing": facing, "width": int(w), "height": int(h)}
        if fl: c["focalLengths"] = [round(x, 4) for x in (num(v) for v in fl) if x]
        if ap: c["apertures"] = [round(x, 3) for x in (num(v) for v in ap) if x]
        if ori is not None and num(ori) is not None: c["orientation"] = int(num(ori))
        if phys and len(phys) == 2 and all(num(v) for v in phys): c["physicalSize"] = [round(num(v), 4) for v in phys]
        out[facing] = c
    try:
        j = json.loads(pkg.text("camera.json") or "null")
    except ValueError:
        j = None
    if isinstance(j, dict) and isinstance(j.get("camera"), list):
        for c in j["camera"]:
            sizes = [s for s in (c.get("streamConfigurationMap") or []) if isinstance(s, list) and len(s) == 2]
            if sizes:
                w, h = max(sizes, key=lambda s: s[0] * s[1])
                add(c.get("facing"), w, h, c.get("focalLengths"), c.get("apertures"))
    elif isinstance(j, list):
        for c in j:
            ch = (c or {}).get("characteristics") or {}
            if c.get("parent_id", -1) not in (-1, None):
                continue
            pa = ch.get("android.sensor.info.pixelArraySize") or []
            if len(pa) == 2:
                add(c.get("facing") or (ch.get("android.lens.facing") or [""])[0], num(pa[0]), num(pa[1]),
                    ch.get("android.lens.info.availableFocalLengths"), ch.get("android.lens.info.availableApertures"),
                    (ch.get("android.sensor.orientation") or [None])[0], ch.get("android.sensor.info.physicalSize"))
    if not out:
        for c in ((device_info(pkg).get("camera") or {}).get("cameras") or []):
            sizes = [tuple(int(x) for x in s.split("x")) for s in (c.get("supportedSizes") or []) if re.match(r"^\d+x\d+$", str(s))]
            if sizes:
                w, h = max(sizes, key=lambda s: s[0] * s[1])
                add(c.get("facing"), w, h)
    return [out[k] for k in ("back", "front") if k in out]


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


def scrub_eng_build_props(bp):
    """清掉工程版采集留下的 eng.* 版本号（有的魔云腾包是从工程机采的，如 vivo V2170A：主指纹里是正式值
    compiler09241555，但各分区 ro.*.build.version.incremental / ro.build.description 却是 eng.compil.*——
    `eng.` 一查就露，而且和主指纹对不上）。做法：从主指纹取正式 incremental，把带 eng. 的 incremental 和
    description 里的 eng 版本号都换成它；厂商分区本来就不同的正式值（compiler…）不动。只在主指纹本身干净时做。"""
    fp = bp.get("ro.build.fingerprint", "")
    parts = fp.split("/")
    if len(parts) < 2:
        return bp
    real_inc = parts[-2].split(":")[0]          # brand/product/device:rel/id/INCREMENTAL:tags
    if not real_inc or re.search(r"eng\.|test-keys|userdebug|redroid", real_inc, re.I):
        return bp                               # 主指纹自己就脏，取不到干净值，别乱改
    out = dict(bp)
    for k, v in bp.items():
        if k.endswith(".build.version.incremental") and re.search(r"eng\.|userdebug", v, re.I):
            out[k] = real_inc
    desc = out.get("ro.build.description", "")
    if re.search(r"eng\.", desc, re.I):
        out["ro.build.description"] = re.sub(r"\beng\.\S+", real_inc, desc)  # 描述里的 eng.* 版本号换成正式值
    return out


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


def normalize_model_json(j, host_gpu, src):
    """兜底导入：已经是机型档的 JSON，补齐必要字段直接用（缺的按板子默认）。"""
    v = j.get("values") or {}
    brand = v.get("brand") or j.get("brand") or ""
    model = v.get("model") or j.get("model") or ""
    mid = j.get("id") or slug(f"{brand}-{model}") or slug(os.path.basename(src)) or "imported-model"
    j.setdefault("values", v)
    j["id"] = mid
    j["schema"] = j.get("schema") or 1
    j["local"] = True
    j["source"] = j.get("source") or "imported-json"
    j["verified"] = bool(j.get("verified"))
    # 显卡家族缺 → 按板子（不伪装显卡，App 看到板子 Mali；一致档）
    if not j.get("gpuFamily"):
        j["gpuFamily"] = host_gpu
        j["tier"] = "consistent"
        j["verifyNote"] = (j.get("verifyNote") or "") + "；⚠兜底：缺显卡数据，按板子显卡处理"
    j.setdefault("tier", "consistent" if j.get("gpuFamily") == host_gpu else "disguised")
    j.setdefault("verifyNote", f"兜底导入自 {os.path.basename(src)}")
    j.setdefault("market", "GLOBAL")
    return j


def convert(path, tac_db, host_gpu, host_cpus, loose=False):
    pkg = Pkg(path)
    if pkg.kind == "model-json":
        return normalize_model_json(pkg.model_json, host_gpu, path), None
    loose = loose or pkg.kind == "loose"
    props = parse_props(pkg.text("props.txt") or pkg.text("adb_debug.prop"))
    if not props.get("ro.product.model"):   # 没有 props.txt（兜底单文件）→ 从 device_info.json 的 prop.Build.* 抽
        for k, v in di_build_props(pkg).items():
            props.setdefault(k, v)
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
    if tree:
        tree = fit_tree_to_host(tree, host_cpus)   # 核数 > 板子的（Tensor G3 / Exynos 等）缩到板子核数，才能挂树（踩坑 #147）
    parts, midrs = cpu_parts_from(tree, pkg.text("cpuinfo") or pkg.text("proc/cpuinfo"))
    disp = display_of(pkg)
    krel, kbuild = kernel_of(pkg)
    sbytes, sgb = storage_of(pkg)
    mid = slug(f"{brand}-{model}")
    if not disp.get("dpi") and mid in DPI_FALLBACK:   # 采集包 dpi 不可信（放大显示录的）时用出厂默认兜底
        disp["dpi"] = DPI_FALLBACK[mid]
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
        "kernelRelease": krel or None, "kernelBuild": kbuild or None, "storageTotal": sgb or None,
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
        if loose:   # 兜底导入：没有真机显卡字符串 → 不伪装显卡，按板子 Mali 当一致档，面板标注
            prof["tier"] = "consistent"; prof["gpuFamily"] = host_gpu
            prof["verifyNote"] += "；⚠兜底：缺真机显卡字符串，显卡按板子处理（App 看到的是板子 Mali）"
        else:
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
    if sbytes:
        prof["dataBytes"] = sbytes
    sens = sensors_of(pkg)
    if sens:
        prof["sensors"] = sens
    # 基带版本、电话模块名（gsm.*，开机后由管家设）；板子名、bootloader、基带平台（ro.*，开机参数）
    rp = {k: props[k] for k in ("gsm.version.baseband", "gsm.version.ril-impl") if props.get(k)}
    if rp:
        prof["radioProps"] = rp
    bpr = {k: props[k] for k in ("ro.product.board", "ro.bootloader", "ro.baseband", "ro.boot.baseband", "ro.boot.bootloader")
           if props.get(k) and re.match(r"^[\w.\-]{1,64}$", props[k])}
    if bpr:
        prof["boardProps"] = bpr
    cams = cameras_of(pkg)
    if cams:
        prof["cameras"] = cams
    cods = codecs_of(pkg)
    if cods:
        prof["codecs"] = cods
    feats = features_of(pkg)
    if feats:
        prof["features"] = feats
    bp = scrub_eng_build_props(build_props_from(pkg, props))
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
    ap.add_argument("--loose", action="store_true", help="兜底导入：缺字段按板子默认补、尽量成一个能用的机型档，不轻易拒绝")
    a = ap.parse_args()
    tp = a.tac or next((p for p in (os.path.join(os.path.dirname(a.out.rstrip("/")), "tac_by_model.json"), "/mmc/mytTemp/resource/imei/tac_by_model.json") if os.path.exists(p)), "")
    tac = json.load(open(tp)) if tp else {}
    for p in a.pkgs:
        try:
            prof, cpu = convert(p, tac, a.host_gpu, a.host_cpus, a.loose)
        except (SystemExit, zipfile.BadZipFile, KeyError, ValueError) as e:
            print(f"✗ {os.path.basename(p.rstrip('/'))}：{e}"); continue
        bp = next(iter(prof.get("buildProps", {}).values()), {})
        mkt = (prof.get("values") or {}).get("marketName", "") or (prof.get("values") or {}).get("model", "")
        fallback = "⚠兜底" if "兜底" in prof.get("verifyNote", "") else ""
        print(f"{'✓' if prof.get('tier') == 'consistent' else '·'} {prof['id']:<26} {mkt[:22]:<22} 安卓{prof.get('androidVersions', [])}"
              f" {prof.get('gpuFamily', '?'):<22} {prof.get('tier', '?'):<10} CPU目录={'有' if cpu else '无'} TAC={len(prof.get('imeiTacs', []))} 分区属性={len(bp)}"
              + (" ⚠CPU不同代" if prof.get("cpuMismatch") else "") + (" " + fallback if fallback else ""))
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
