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
}

// File 一个伪造文件：容器里的路径 → 内容。
type File struct {
	Target  string // 容器里要盖住的路径
	Name    string // 在伪造目录里的文件名
	Content string
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
	if len(s.CPUParts) > 0 {
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
	return files, nil
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
		if err := os.WriteFile(filepath.Join(dir, f.Name), []byte(f.Content), 0o444); err != nil {
			return err
		}
	}
	return nil
}
