package fakefs

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// CPUTree 一份真机的 /sys/devices/system/cpu 整个目录（模板，profiles/cpu/<名>.json，从真机采集转换来，踩坑 #147）。
//
// 为什么整个目录换：魔云腾内核把 /sys/devices/system/cpu 下的属性文件 stat 成目录，单个文件 bind 挂不上
// （ENOTDIR），以前的 midr / 频率伪装从来没生效；而且只换几个文件，频率表、调频驱动名、分组（policy0/4/7）、
// 空闲驱动这些还是 RK3588 的。整个目录挂一份真机的，一次全对上，文件类型也恢复成普通文件。
type CPUTree struct {
	Name   string            `json:"name"`   // 模板名（一般是芯片：mt6895）
	SoC    string            `json:"soc"`    // 芯片型号（和机型档案 socModel 对应）
	Source string            `json:"source"` // 数据来源（哪台真机、怎么采的）
	Files  map[string]string `json:"files"`  // 相对路径 → 内容
	Links  map[string]string `json:"links"`  // 相对路径 → 链接目标（真机 sysfs 里的符号链接，例如 cpu0/cpufreq → ../cpufreq/policy0）
	Dirs   []string          `json:"dirs"`   // 空目录
}

var cpuDirRe = regexp.MustCompile(`^cpu([0-9]+)$`)

// CPUs 模板里有几个核（cpuN 目录数）。
func (t *CPUTree) CPUs() int {
	seen := map[string]bool{}
	add := func(p string) {
		if first, _, _ := strings.Cut(p, "/"); cpuDirRe.MatchString(first) {
			seen[first] = true
		}
	}
	for p := range t.Files {
		add(p)
	}
	for p := range t.Links {
		add(p)
	}
	for _, p := range t.Dirs {
		add(p)
	}
	return len(seen)
}

// MIDRs 每个核的 midr_el1（按核序号），用来生成和目录一致的 /proc/cpuinfo。
func (t *CPUTree) MIDRs() ([]uint64, error) {
	n := t.CPUs()
	out := make([]uint64, n)
	for i := 0; i < n; i++ {
		raw, ok := t.Files[fmt.Sprintf("cpu%d/regs/identification/midr_el1", i)]
		if !ok {
			return nil, fmt.Errorf("CPU 模板 %s 缺 cpu%d 的 midr_el1", t.Name, i)
		}
		v, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(raw), "0x"), 16, 64)
		if err != nil {
			return nil, fmt.Errorf("CPU 模板 %s 的 cpu%d midr_el1 %q 不对", t.Name, i, raw)
		}
		out[i] = v
	}
	return out, nil
}

// Parts 把每个核的 midr 按连续相同的分组（cpuinfo 用）。
func (t *CPUTree) Parts() ([]CPUPart, error) {
	midrs, err := t.MIDRs()
	if err != nil {
		return nil, err
	}
	var parts []CPUPart
	for _, m := range midrs {
		s := fmt.Sprintf("0x%08x", m)
		if len(parts) > 0 && parts[len(parts)-1].MIDR == s {
			parts[len(parts)-1].Count++
			continue
		}
		parts = append(parts, CPUPart{MIDR: s, Count: 1})
	}
	return parts, nil
}

// Validate 路径都要是目录里面的相对路径（不能有 ..、绝对路径），至少要有每个核的 midr。
func (t *CPUTree) Validate() error {
	check := func(p string) error {
		if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") {
			return fmt.Errorf("CPU 模板 %s 的路径 %q 不对", t.Name, p)
		}
		return nil
	}
	for p := range t.Files {
		if err := check(p); err != nil {
			return err
		}
	}
	for p := range t.Links {
		if err := check(p); err != nil {
			return err
		}
	}
	for _, p := range t.Dirs {
		if err := check(p); err != nil {
			return err
		}
	}
	if t.CPUs() == 0 {
		return fmt.Errorf("CPU 模板 %s 里没有 cpuN 目录", t.Name)
	}
	_, err := t.MIDRs()
	return err
}

// 真机 sysfs 里能写的（其余 0444）：权限位也照真机，App 能 stat 到
var writableSysfs = map[string]bool{
	"online": true, "uevent": true, "scaling_governor": true, "scaling_max_freq": true, "scaling_min_freq": true,
	"scaling_setspeed": true, "disable": true, "control": true, "autosuspend_delay_ms": true,
}

// write 在 dir 下按模板建整个目录。
func (t *CPUTree) write(dir string) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, d := range t.Dirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(t.Files))
	for p := range t.Files {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		switch {
		case t.Files[p] == "":
			mode = 0o400 // 采集时普通权限读不了的：App 读也得到「没权限」，和真机一样，不是读到空内容
		case writableSysfs[path.Base(p)]:
			mode = 0o644
		}
		if err := os.WriteFile(full, []byte(t.Files[p]), mode); err != nil {
			return err
		}
		_ = os.Chmod(full, mode) // umask 不影响
	}
	for p, target := range t.Links {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		_ = os.Remove(full)
		if err := os.Symlink(target, full); err != nil {
			return err
		}
	}
	// SELinux 标签在宿主机这边直接写好（真机这棵树统一是 sysfs_devices_system_cpu）。不能等挂进手机再补：
	// 魔云腾内核按路径把 /sys/devices/system/cpu 下的东西都当目录处理，手机里给文件打标签全失败，
	// 文件、链接留成 unlabeled → 谷歌服务等 App 读 possible / online 被 SELinux 拒（踩坑 #147）。
	// 宿主机没开 SELinux 时写失败也无所谓（手机没开 SELinux 用不上）
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			_ = lsetxattr(p, "security.selinux", CPUTreeLabel)
		}
		return nil
	})
	return nil
}

// CPUTreeLabel 真机 /sys/devices/system/cpu 下所有文件、目录、链接的 SELinux 标签（AOSP genfs_contexts）。
const CPUTreeLabel = "u:object_r:sysfs_devices_system_cpu:s0"

// lsetxattr 不跟随链接（链接自己也要标签：App 走 cpu0/cpufreq → ../cpufreq/policy0 要能读链接）。
func lsetxattr(p, name, value string) error {
	pb, err := syscall.BytePtrFromString(p)
	if err != nil {
		return err
	}
	nb, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	v := append([]byte(value), 0)
	if _, _, e := syscall.Syscall6(syscall.SYS_LSETXATTR, uintptr(unsafe.Pointer(pb)), uintptr(unsafe.Pointer(nb)),
		uintptr(unsafe.Pointer(&v[0])), uintptr(len(v)), 0, 0); e != 0 {
		return e
	}
	return nil
}
