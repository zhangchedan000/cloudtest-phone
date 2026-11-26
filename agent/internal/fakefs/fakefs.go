// Package fakefs 生成每台云手机的「伪造系统文件」：/proc/cpuinfo、/proc/version、
// /proc/sys/kernel/osrelease 等。开机后挂载到容器里对应位置，App 读到的就是机型档案里的值。
//
// 原则：只改「身份」字段（CPU 型号、内核版本号、频率），不改真实能力相关的字段
// （例如 cpuinfo 的 Features 行照抄宿主机——App 可以用 getauxval(AT_HWCAP) 对照，改了反而矛盾）。
package fakefs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CPUPart 机型档案里的一组 CPU 核心（例如 4 个 A55）。
type CPUPart struct {
	Part  string `json:"part"`
	MIDR  string `json:"midr"`
	Count int    `json:"count"`
}

// Spec 生成伪造文件需要的全部值（来自机型档案 + 设备参数）。
type Spec struct {
	CPUParts      []CPUPart
	CPUMaxFreqKHz []int
	KernelRelease string // 例如 5.10.198-android12-9-gc2f7e1a40b6d
	KernelBuild   string // 例如 #1 SMP PREEMPT Mon Jun 3 08:12:45 UTC 2025
	HostCPUInfo   string // 宿主机真实 /proc/cpuinfo（用来照抄 Features 等能力字段）
	// CPUTree 有就整个 /sys/devices/system/cpu 换成这份真机目录，cpuinfo 的核心型号也按它的 midr 出（踩坑 #147）；
	// 这时 CPUParts / CPUMaxFreqKHz 不用。
	CPUTree *CPUTree
	// BootID 这次开机的 boot_id（每台手机每次开机一个新的 UUID）。容器共用宿主机内核，
	// /proc/sys/kernel/random/boot_id 所有手机读到的都一样——App 不要权限就能读，一对比就知道是同一台机器（踩坑 #79）。
	BootID string
	// DeviceTreeModel / DeviceTreeCompatible 盖住 /proc/device-tree 和 /sys/firmware/devicetree/base 下的
	// model / compatible——真 RK3588 板子在这里写着 "Linux Q1" / "rockchip,rk3588"，暴露宿主硬件（踩坑 #157）。
	// 普通 App（untrusted_app）读不到这俩（sysfs 标签、SELinux 挡，真机同样挡），这是给 root / 深度检测的防御纵深；
	// 留空就不盖。值是设备树属性，按 NUL 结尾字符串写（compatible 可含多段，用 \x00 分隔）。
	DeviceTreeModel      string
	DeviceTreeCompatible string
	// GPURenderer 机型声称的显卡名（GL renderer，如 "Mali-G720-Immortalis MC12"）。盖住
	// /sys/class/misc/mali0/device/gpuinfo——板子真内核在这里写 "Mali-G610 4 cores r0p0 0xA867"，
	// 和伪装档声称的显卡名（GL/Vulkan 已改）对不上就露馅（踩坑 #164）。非 Mali / 空则不盖。
	GPURenderer string
	// GPUCoresFallback 板子真实的显卡核心数（读板子 gpuinfo 得来，Mali-G610 = 4）。renderer 里没有 MCn
	// 核心数标注的机型（Pixel 的 Mali-G715/G78、三星 Mali-G720-Immortalis 等）用它兜底：至少把 gpuinfo
	// 型号名盖成声称的、核心数用板子真实的（反正性能就是板子那几个核），免得只因缺 MCn 就还露「Mali-G610」。
	GPUCoresFallback int
}

// File 一个伪造文件：容器里的路径 → 内容。
type File struct {
	Target  string // 容器里要盖住的路径
	Name    string // 在伪造目录里的文件名
	Content string
	Tree    *CPUTree // 不是单个文件而是整个目录（Target 是目录）
}

// Build 生成全部伪造文件。缺数据的项跳过（不生成半真半假的内容）。
func Build(s Spec) ([]File, error) {
	var files []File
	// /proc/cmdline：默认是宿主机云主机的内核命令行（含 oracle / cloudimg / iscsi / nvme，
	// 一看就是服务器），换成一条通用的安卓启动命令行，和伪装属性一致（verifiedbootstate=green 等）。
	files = append(files, File{
		Target:  "/proc/cmdline",
		Name:    "cmdline",
		Content: "bootopt=64S3,32N2,64N2 androidboot.verifiedbootstate=green androidboot.veritymode=enforcing buildvariant=user\n",
	})
	if s.BootID != "" {
		files = append(files, File{Target: "/proc/sys/kernel/random/boot_id", Name: "boot_id", Content: s.BootID + "\n"})
	}
	if s.CPUTree != nil {
		parts, err := s.CPUTree.Parts()
		if err != nil {
			return nil, err
		}
		cpuinfo, _, err := cpuInfo(parts, s.HostCPUInfo)
		if err != nil {
			return nil, err
		}
		files = append(files,
			File{Target: "/proc/cpuinfo", Name: "cpuinfo", Content: cpuinfo},
			File{Target: "/sys/devices/system/cpu", Name: "cpu", Tree: s.CPUTree},
		)
	} else if len(s.CPUParts) > 0 {
		cpuinfo, midrs, err := cpuInfo(s.CPUParts, s.HostCPUInfo)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Target: "/proc/cpuinfo", Name: "cpuinfo", Content: cpuinfo})
		for i, m := range midrs {
			files = append(files, File{
				Target:  fmt.Sprintf("/sys/devices/system/cpu/cpu%d/regs/identification/midr_el1", i),
				Name:    fmt.Sprintf("midr_el1.cpu%d", i),
				Content: fmt.Sprintf("0x%016x\n", m),
			})
		}
	}
	for i, khz := range s.CPUMaxFreqKHz {
		if s.CPUTree != nil {
			break
		}
		v := strconv.Itoa(khz) + "\n"
		files = append(files,
			File{Target: fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/cpuinfo_max_freq", i), Name: fmt.Sprintf("cpuinfo_max_freq.cpu%d", i), Content: v},
			File{Target: fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/scaling_max_freq", i), Name: fmt.Sprintf("scaling_max_freq.cpu%d", i), Content: v},
		)
	}
	if s.KernelRelease != "" {
		build := s.KernelBuild
		if build == "" {
			build = "#1 SMP PREEMPT"
		}
		files = append(files,
			File{Target: "/proc/sys/kernel/osrelease", Name: "osrelease", Content: s.KernelRelease + "\n"},
			File{Target: "/proc/sys/kernel/version", Name: "version", Content: build + "\n"},
			File{Target: "/proc/version", Name: "proc_version", Content: ProcVersion(s.KernelRelease, build)},
		)
	}
	// 设备树 model / compatible：盖住宿主机真板子名（Linux Q1 / rockchip,rk3588，踩坑 #157）。
	// 设备树属性是 NUL 结尾字符串，内容直接写文件（不经 JSON），NUL 能保留。/proc/device-tree 可能是
	// /sys/firmware/devicetree/base 的软链，两条路径都试挂（真路径挂上了，软链那条挂不上会被跳过，不影响）。
	for _, dt := range []struct{ val, base string }{
		{s.DeviceTreeModel, "model"},
		{s.DeviceTreeCompatible, "compatible"},
	} {
		if dt.val == "" {
			continue
		}
		content := dt.val + "\x00"
		files = append(files,
			File{Target: "/sys/firmware/devicetree/base/" + dt.base, Name: "dt_" + dt.base + "_sys", Content: content},
			File{Target: "/proc/device-tree/" + dt.base, Name: "dt_" + dt.base + "_proc", Content: content},
		)
	}
	// 显卡 gpuinfo：盖住板子真内核报的 "Mali-G610 4 cores r0p0 0xA867"（踩坑 #164）。
	// /sys/class/misc/mali0/device 是软链，bind 到这条稳定路径即盖住真实 inode（/sys/devices/platform/<地址>.gpu/gpuinfo），
	// 两条读法都生效；标签 sysfs（App 可读）由小助手照新挂 sysfs 同路径补（label.go）。
	if gi := MaliGPUInfo(s.GPURenderer, s.GPUCoresFallback); gi != "" {
		files = append(files, File{Target: "/sys/class/misc/mali0/device/gpuinfo", Name: "gpuinfo", Content: gi})
	}
	return files, nil
}

// maliGPUID 和板子（RK3588 = Mali-G610 → 0xA867）同代的 Valhall 显卡产品 ID：
// 按 Linux panthor 驱动的 GPU_PROD_ID 表推（低位 product_major：G610=7/G710=2/G510=3/G310=4，高位同板子 0xA86）。
// 别代（G720/G925 第五代 Immortalis、老的 G52/G57/G77……）公开值不可靠，不写 ID，只写型号+核心数。
var maliGPUID = map[string]string{"G310": "0xA864", "G510": "0xA863", "G610": "0xA867", "G710": "0xA862"}

var (
	reMaliCores  = regexp.MustCompile(`M[CP](\d+)`)     // 核心数：MC12 / MP4
	reMaliModel  = regexp.MustCompile(`Mali-(G\d+)`)    // 基础型号：G610
	reMaliSuffix = regexp.MustCompile(`\s*M[CP]\d+.*$`) // " MC12" 及其后缀
)

// MaliGPUInfo 按机型声称的显卡名（GL renderer）生成 /sys/.../mali0/device/gpuinfo 内容，盖掉板子真实值。
// 格式照板子内核 kbase 驱动："Mali-<型号> <核心数> cores [r0p0 0x<产品ID>]"。核心数先从 renderer 的 MCn/MPn 取
// （天玑机型的 renderer 都带，如 "Mali-G720-Immortalis MC12" → 12 核）；没带的（Pixel 的 Mali-G715/G78、
// 三星 Mali-G720-Immortalis 等）用 fallbackCores（板子真实核心数）兜底——至少把型号名盖对，别因缺 MCn 还露板子
// 的「Mali-G610」。产品 ID 只对认得的同代 Valhall 写（认不出就省掉，真机不同内核版本格式本就有出入，比写错 ID 稳）。
// renderer 不是 Mali（高通 Adreno 等）返回空——那种机型压根不该有 mali0 节点，是另一层更深的问题，不在这造 Mali 内容。
// 核心数既解析不出、也没 fallback 返回空（只改型号、核心数空着格式不对）。
func MaliGPUInfo(renderer string, fallbackCores int) string {
	if !strings.Contains(renderer, "Mali-") {
		return ""
	}
	cores := fallbackCores
	if cm := reMaliCores.FindStringSubmatch(renderer); cm != nil {
		cores, _ = strconv.Atoi(cm[1])
	}
	if cores <= 0 {
		return ""
	}
	name := strings.TrimSpace(reMaliSuffix.ReplaceAllString(renderer, "")) // 去掉 " MC12" 后缀 → "Mali-G720-Immortalis"（没 MCn 的原样留）
	line := fmt.Sprintf("%s %d cores", name, cores)
	if mm := reMaliModel.FindStringSubmatch(name); mm != nil {
		if id, ok := maliGPUID[mm[1]]; ok {
			line += " r0p0 " + id
		}
	}
	return line + "\n"
}

// ProcVersion 按安卓 GKI 内核的真实格式拼 /proc/version。编译器版本跟内核分支对应。
func ProcVersion(release, build string) string {
	toolchain := "Android (7284624, based on r416183b) clang version 12.0.5 (https://android.googlesource.com/toolchain/llvm-project c935d99d7cf2016289302412d708641d52d2f7ee), LLD 12.0.5 (/buildbot/src/android/llvm-toolchain/out/llvm-project/lld c935d99d7cf2016289302412d708641d52d2f7ee)"
	switch {
	case strings.Contains(release, "android14"), strings.HasPrefix(release, "6.1."):
		toolchain = "Android (10087095, +pgo, +bolt, +lto, -mlgo, based on r487747c) clang version 17.0.2 (https://android.googlesource.com/toolchain/llvm-project d9f89f4d16663d5012e5c09495f3b30ece3d2362), LLD 17.0.2"
	case strings.Contains(release, "android13"), strings.HasPrefix(release, "5.15."):
		toolchain = "Android (8508608, based on r450784e) clang version 14.0.7 (https://android.googlesource.com/toolchain/llvm-project 4c603efb0cca074e9238af8b4106c30add4418f6), LLD 14.0.7"
	}
	return fmt.Sprintf("Linux version %s (build-user@build-host) (%s) %s\n", release, toolchain, build)
}

// cpuInfo 生成 arm64 格式的 /proc/cpuinfo：每个核一段，身份字段来自 MIDR，能力字段照抄宿主机。
func cpuInfo(parts []CPUPart, host string) (string, []uint64, error) {
	features := hostField(host, "Features")
	bogo := hostField(host, "BogoMIPS")
	if bogo == "" {
		bogo = "26.00"
	}
	var b strings.Builder
	var midrs []uint64
	n := 0
	for _, p := range parts {
		midr, err := strconv.ParseUint(strings.TrimPrefix(p.MIDR, "0x"), 16, 64)
		if err != nil {
			return "", nil, fmt.Errorf("MIDR %q 不对：%w", p.MIDR, err)
		}
		for i := 0; i < p.Count; i++ {
			fmt.Fprintf(&b, "processor\t: %d\n", n)
			fmt.Fprintf(&b, "BogoMIPS\t: %s\n", bogo)
			if features != "" {
				fmt.Fprintf(&b, "Features\t: %s\n", features)
			}
			fmt.Fprintf(&b, "CPU implementer\t: 0x%02x\n", (midr>>24)&0xff)
			fmt.Fprintf(&b, "CPU architecture: 8\n")
			fmt.Fprintf(&b, "CPU variant\t: 0x%x\n", (midr>>20)&0xf)
			fmt.Fprintf(&b, "CPU part\t: 0x%03x\n", (midr>>4)&0xfff)
			fmt.Fprintf(&b, "CPU revision\t: %d\n\n", midr&0xf)
			midrs = append(midrs, midr)
			n++
		}
	}
	return b.String(), midrs, nil
}

func hostField(cpuinfo, key string) string {
	for _, line := range strings.Split(cpuinfo, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Write 把伪造文件写到目录（每次开机前重写，保证和最新参数一致）。
func Write(dir string, files []File) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		if f.Tree != nil {
			if err := f.Tree.write(filepath.Join(dir, f.Name)); err != nil {
				return err
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, f.Name), []byte(f.Content), 0o444); err != nil {
			return err
		}
	}
	return nil
}
