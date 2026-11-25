package phonecmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unsafe"
)

// 开机前给挂进手机的东西补 SELinux 标签（踩坑 #135）：管家从宿主机挂进来的文件（改写过的 build.prop、伪造的
// /proc 文件、Docker 的 /etc/hosts…）和 Docker 建的 /dev 设备节点都没有手机认的标签（宿主机写的标签手机那套不认，
// 得在手机自己的 SELinux 命名空间里写）。按安卓自己的 file_contexts 算，规则和 libselinux 一样：
// 带正则元字符的条目保持原顺序在前、纯路径条目挪到后面，从后往前第一个匹配的生效；整串匹配；按文件类型过滤。

type spec struct {
	re    *regexp.Regexp
	ftype byte // 0 = 任意；'-' 普通文件，'d' 目录，'l' 链接，'c' 字符设备，'b' 块设备，'s' 套接字，'p' 管道
	ctx   string
}

// Labeler 一套 file_contexts。
type Labeler struct{ specs []spec }

// FileContextFiles 安卓按这个顺序读 file_contexts。
var FileContextFiles = []string{
	"/system/etc/selinux/plat_file_contexts",
	"/system_ext/etc/selinux/system_ext_file_contexts",
	"/product/etc/selinux/product_file_contexts",
	"/vendor/etc/selinux/vendor_file_contexts",
	"/odm/etc/selinux/odm_file_contexts",
}

func hasMeta(s string) bool { return strings.ContainsAny(s, `.^$?*+|[({\`) }

// LoadLabeler 读 file_contexts（读不到的跳过）。
func LoadLabeler(files []string) (*Labeler, error) {
	var meta, exact []spec
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) < 2 || strings.HasPrefix(fs[0], "#") {
				continue
			}
			s := spec{ctx: fs[len(fs)-1]}
			if len(fs) >= 3 && len(fs[1]) == 2 && fs[1][0] == '-' {
				s.ftype = fs[1][1]
			}
			re, err := regexp.Compile("^(?:" + fs[0] + ")$")
			if err != nil {
				continue
			}
			s.re = re
			if hasMeta(fs[0]) {
				meta = append(meta, s)
			} else {
				exact = append(exact, s)
			}
		}
		fh.Close()
	}
	return &Labeler{specs: append(meta, exact...)}, nil
}

func ftypeOf(m os.FileMode) byte {
	switch {
	case m.IsDir():
		return 'd'
	case m&os.ModeSymlink != 0:
		return 'l'
	case m&os.ModeCharDevice != 0:
		return 'c'
	case m&os.ModeDevice != 0:
		return 'b'
	case m&os.ModeSocket != 0:
		return 's'
	case m&os.ModeNamedPipe != 0:
		return 'p'
	}
	return '-'
}

// Lookup path 应有的标签（"" = 不打）。
func (l *Labeler) Lookup(path string, mode os.FileMode) string {
	ft := ftypeOf(mode)
	for i := len(l.specs) - 1; i >= 0; i-- {
		s := l.specs[i]
		if s.ftype != 0 && s.ftype != ft {
			continue
		}
		if s.re.MatchString(path) {
			if s.ctx == "<<none>>" {
				return ""
			}
			return s.ctx
		}
	}
	return ""
}

// Relabel 给 path（目录就递归）打标签，返回打了几个。skip 里的路径和它下面的都不碰。
func (l *Labeler) Relabel(path string, recursive bool, skip map[string]bool) int {
	n := 0
	walk := func(p string, fi os.FileInfo) {
		if ctx := l.Lookup(p, fi.Mode()); ctx != "" && setLabel(p, ctx) == nil {
			n++
		}
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	walk(path, fi)
	if recursive && fi.IsDir() {
		_ = filepath.Walk(path, func(p string, fi os.FileInfo, err error) error {
			if err != nil || p == path {
				return nil
			}
			if skip[p] {
				if fi.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			walk(p, fi)
			return nil
		})
	}
	return n
}

// setLabel 打标签；挂载点是只读 bind（build.prop、伪造文件）时临时改可写、打完改回只读。
func setLabel(p, ctx string) error {
	err := lsetxattr(p, ctx)
	if err != syscall.EROFS {
		return err
	}
	if e := syscall.Mount("", p, "", syscall.MS_REMOUNT|syscall.MS_BIND, ""); e != nil {
		return err
	}
	err = lsetxattr(p, ctx)
	_ = syscall.Mount("", p, "", syscall.MS_REMOUNT|syscall.MS_BIND|syscall.MS_RDONLY, "")
	return err
}

func lsetxattr(p, ctx string) error {
	pb, err := syscall.BytePtrFromString(p)
	if err != nil {
		return err
	}
	nb, _ := syscall.BytePtrFromString("security.selinux")
	v := append([]byte(ctx), 0)
	_, _, e := syscall.Syscall6(syscall.SYS_LSETXATTR, uintptr(unsafe.Pointer(pb)), uintptr(unsafe.Pointer(nb)), uintptr(unsafe.Pointer(&v[0])), uintptr(len(v)), 0, 0)
	if e != 0 {
		return e
	}
	return nil
}

func lgetxattr(p string) string {
	buf := make([]byte, 256)
	pb, err := syscall.BytePtrFromString(p)
	if err != nil {
		return ""
	}
	nb, _ := syscall.BytePtrFromString("security.selinux")
	r, _, e := syscall.Syscall6(syscall.SYS_LGETXATTR, uintptr(unsafe.Pointer(pb)), uintptr(unsafe.Pointer(nb)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)
	if e != 0 || r == 0 {
		return ""
	}
	return strings.TrimRight(string(buf[:r]), "\x00")
}

type mount struct{ point, fstype string }

func isMount(all []mount, p string) bool {
	for _, m := range all {
		if m.point == p {
			return true
		}
	}
	return false
}

// mounts 这个挂载命名空间里的挂载（/proc/self/mountinfo：第 5 列挂载点，" - " 后第一列文件系统类型）。
func mounts() []mount {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	var out []mount
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		m := mount{point: strings.ReplaceAll(f[4], `\040`, " ")}
		for i := 5; i+1 < len(f); i++ {
			if f[i] == "-" {
				m.fstype = f[i+1]
				break
			}
		}
		out = append(out, m)
	}
	return out
}

// 伪造的 sysfs 目录照真机的类型打（真机这些节点在 /sys/devices/... 下，规则按设备路径单独标）
var fakeSysLabels = map[string]string{
	"/sys/class/power_supply": "u:object_r:sysfs_batteryinfo:s0",
	"/sys/class/thermal":      "u:object_r:sysfs_thermal:s0",
}

// 内核自己的文件系统（标签由规则里的 genfscon 定）：这些挂载不碰
var kernelFS = map[string]bool{"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true, "tmpfs": true, "devpts": true,
	"mqueue": true, "selinuxfs": true, "binder": true, "debugfs": true, "tracefs": true, "bpf": true, "pstore": true,
	"configfs": true, "fusectl": true, "securityfs": true, "functionfs": true, "overlay": true}

// BindsFile 管家写的「挂进手机的路径」清单（一行一个）：手机的挂载表被 pp_mnt 改写过（宿主机挂进来的 bind 都藏了），
// 小助手照挂载表找不全，所以管家直接告诉它。
const BindsFile = "/data/.ppc/binds"

// LabelBoot 开机前的补标签：宿主机挂进来的文件 / 目录（Docker bind，文件系统是宿主机的盘）+ /dev 整个
// + 容器根目录 + /metadata（容器那一层里以前没开 SELinux 时建的，没有标签）。
// 挂在 /proc、/sys 下面的伪造文件用新挂一份 proc / sysfs 里同一路径的标签（genfscon 定的，比如 proc_cpuinfo）。
func LabelBoot() (int, error) { return label(true) }

// LabelBinds 开机后管家又挂了伪造文件：只补管家清单里的挂载。/dev 这时不能再整个重打：安卓 init 建的属性文件
// （/dev/__properties__/<类型>）、套接字的标签是按类型现打的，file_contexts 里没有，重打会全变成 device。
func LabelBinds() (int, error) { return label(false) }

// LabelTree 把 dir 下所有文件、目录、链接打成 ctx（mp 是 dir 所在的只读挂载点，临时改可写、打完改回只读）。
// 给整棵伪造的 CPU 目录用：必须在手机里、在它挂到 /sys/devices/system/cpu 之前打——宿主机写的标签手机里不认，
// 挂过去以后魔云腾内核又让文件打不上（踩坑 #147）
func LabelTree(mp, dir, ctx string) (int, error) {
	if !strings.HasPrefix(dir+"/", mp+"/") {
		return 0, fmt.Errorf("%s 不在 %s 下", dir, mp)
	}
	if err := syscall.Mount("", mp, "", syscall.MS_REMOUNT|syscall.MS_BIND, ""); err != nil {
		return 0, fmt.Errorf("改可写失败：%w", err)
	}
	defer func() { _ = syscall.Mount("", mp, "", syscall.MS_REMOUNT|syscall.MS_BIND|syscall.MS_RDONLY, "") }()
	n := 0
	var first error
	err := filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if e := lsetxattr(p, ctx); e != nil {
			if first == nil {
				first = fmt.Errorf("%s：%w", p, e)
			}
			return nil
		}
		n++
		return nil
	})
	if err == nil && first != nil && n == 0 {
		err = first
	}
	return n, err
}

func label(boot bool) (int, error) {
	l, err := LoadLabeler(FileContextFiles)
	if err != nil {
		return 0, err
	}
	tmp := "/dev/.pplbl"
	_ = os.MkdirAll(tmp+"/proc", 0o700)
	_ = os.MkdirAll(tmp+"/sys", 0o700)
	procOK := syscall.Mount("proc", tmp+"/proc", "proc", 0, "") == nil
	sysOK := syscall.Mount("sysfs", tmp+"/sys", "sysfs", 0, "") == nil
	defer func() {
		_ = syscall.Unmount(tmp+"/proc", syscall.MNT_DETACH)
		_ = syscall.Unmount(tmp+"/sys", syscall.MNT_DETACH)
		_ = os.RemoveAll(tmp)
	}()
	// 伪造文件：照真文件（新挂的 proc / sysfs 里同一路径，没有就往上找父目录）的标签；
	// 真机上电池、温度节点在设备目录下、规则里单独标了类型，这里照真机打
	fromKernel := func(mp, root string) int {
		n := 0
		// 整个目录挂进来的（真机 CPU 目录 /sys/devices/system/cpu）：挂之前已经由 __pp_labeltree 在中转目录里整棵打好
		// （挂过去以后文件打不上，踩坑 #147），这里不再按新挂的 sysfs 重打（会把链接改成 sysfs，和真机不一样）
		if fi, err := os.Lstat(mp); err == nil && fi.IsDir() {
			return 0
		}
		_ = filepath.Walk(mp, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			ref := root + p[len(root[len(tmp):]):]
			ctx := ""
			for prefix, c := range fakeSysLabels {
				if strings.HasPrefix(p, prefix) {
					ctx = c
				}
			}
			if ctx != "" {
				if setLabel(p, ctx) == nil {
					n++
				}
				return nil
			}
			// 照新挂的 sysfs / proc 里同一路径的标签；打不上（魔云腾内核 /sys/devices/system/cpu 下的属性文件是怪的，
			// 读出来的标签手机策略不认，踩坑 #147）就退到上一级目录的标签再试
			for q := ref; len(q) > len(tmp); q = filepath.Dir(q) {
				if c := lgetxattr(q); c != "" && setLabel(p, c) == nil {
					n++
					break
				}
			}
			return nil
		})
		return n
	}
	n := 0
	var all []mount
	if boot {
		all = mounts()
	}
	if b, err := os.ReadFile(BindsFile); err == nil {
		seen := map[string]bool{}
		for _, m := range all {
			seen[m.point] = true
		}
		for _, p := range strings.Split(string(b), "\n") {
			if p = strings.TrimSpace(p); p != "" && !seen[p] {
				all = append(all, mount{point: p, fstype: "bind"})
			}
		}
	}
	for _, m := range all {
		mp := m.point
		// 伪造的 /proc、/sys 内容（tmpfs / 宿主机文件 bind）照真文件打；内核自己的文件系统（proc、sysfs、cgroup、
		// /sys/kernel/tracing 的 tracefs、pstore…）标签由规则定，不碰
		fake := (strings.HasPrefix(mp, "/proc/") || strings.HasPrefix(mp, "/sys/")) && !strings.HasPrefix(mp, "/sys/fs/cgroup") &&
			(m.fstype == "tmpfs" || !kernelFS[m.fstype]) && !strings.HasPrefix(m.fstype, "cgroup")
		if !fake && (kernelFS[m.fstype] || mp == "/" || mp == "/data" || strings.HasPrefix(mp, "/data/.pp") || strings.HasPrefix(mp, tmp)) {
			continue
		}
		switch {
		case strings.HasPrefix(mp, "/proc/"):
			if procOK {
				n += fromKernel(mp, tmp+"/proc")
			}
		case strings.HasPrefix(mp, "/sys/"):
			if sysOK {
				n += fromKernel(mp, tmp+"/sys")
			}
		default:
			n += l.Relabel(mp, true, nil)
			// 伪装档挂成真机名字的 Vulkan 入口模块（vulkan.adreno.so……）：规则里只有 vulkan.mali.so 单独标成 App 能加载的
			// same_process_hal_file，别的名字按目录规则成了 vendor_hal_file，App 加载不了 Vulkan
			// .vkreal.so 是伪装档 Vulkan 转接库 dlopen 的真 Mali 驱动（隐藏点文件），也要能被 App 进程加载
			if strings.HasPrefix(mp, "/vendor/lib64/hw/vulkan.") || strings.HasPrefix(mp, "/vendor/lib/hw/vulkan.") ||
				mp == "/vendor/lib64/hw/.vkreal.so" {
				if setLabel(mp, "u:object_r:same_process_hal_file:s0") == nil {
					n++
				}
			}
			// 挂的是镜像里原来没有的文件名（伪装档的 libGLES_adreno.so、gralloc.qcom.so……）：docker 要在容器可写层里建
			// 挂载点，所在目录被复制上来一份，没有标签（unlabeled）→ 显存分配等服务进不了 /vendor/lib64/hw，整台开不了机。
			// 往上每一级目录都被复制上来了（/vendor/lib/hw、/vendor/lib、/vendor 都实测是 unlabeled），逐级按规则补标签（不递归）
			if strings.HasPrefix(mp, "/vendor/") || strings.HasPrefix(mp, "/system/") || strings.HasPrefix(mp, "/odm/") || strings.HasPrefix(mp, "/product/") {
				for d := filepath.Dir(mp); d != "/" && d != "."; d = filepath.Dir(d) {
					n += l.Relabel(d, false, nil)
				}
			}
		}
	}
	if boot {
		// 安卓开机往根目录写的 /metadata、/cache：容器根目录是 overlay，运行时新建 / 拷上来的文件在手机的 SELinux
		// 命名空间里一律没有标签（overlay 用 Docker 的身份建上层文件），aconfigd 读不了 → keystore2 崩 → init 关机。
		// 真机这两个是单独分区；这里各挂一块内存盘（容器每次开机都是新的一层，本来就不留东西），里面新建的文件按规则打标签
		for _, d := range []string{"/metadata", "/cache"} {
			if !isMount(all, d) {
				_ = os.MkdirAll(d, 0o755)
				_ = syscall.Mount("tmpfs", d, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, "mode=0755")
			}
			n += l.Relabel(d, true, nil)
		}
		// redroid 带的开源 Vulkan 驱动（PC 显卡 radeon / nouveau、高通 freedreno、树莓派 broadcom……）：App 能列 /vendor/lib64/hw，
		// 一看就不是天玑手机；镜像里删不掉（构建时跑不了安卓程序），每次开机在这里删（容器每次开机是新的一层，等于没有）。
		// Vulkan 用镜像补上的 Mali 入口模块 vulkan.mali.so（ro.hardware.vulkan=mali，踩坑 #148）
		for _, lib := range []string{"lib64", "lib"} {
			for _, d := range []string{"broadcom", "freedreno", "lvp", "nouveau", "panfrost", "pastel", "radeon", "virtio", "intel", "v3d", "asahi"} {
				_ = os.Remove("/vendor/" + lib + "/hw/vulkan." + d + ".so")
			}
		}
		// 伪装档挂了真机名字的 Vulkan 入口模块（vulkan.adreno.so，是镜像 vulkan.mali.so 的一份拷贝）：镜像自带的 vulkan.mali.so
		// 也删掉——高通真机的目录里只有 vulkan.adreno.so，两个都在一看就不对
		if ms, _ := filepath.Glob("/vendor/lib64/hw/vulkan.*.so"); len(ms) > 1 {
			_ = os.Remove("/vendor/lib64/hw/vulkan.mali.so")
		}
		// Docker 的 /dev/ptmx 是指向 /dev/pts/ptmx 的链接（标签是 devpts，ioctl 按扩展权限只放行 ptmx_device）：
		// 安卓 logwrapper 开伪终端被拦 → linkerconfig 起不来 → 系统程序找不到 APEX 里的库。换成真机那样的字符设备 5:2
		if fi, err := os.Lstat("/dev/ptmx"); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			if os.Remove("/dev/ptmx") == nil {
				_ = syscall.Mknod("/dev/ptmx", syscall.S_IFCHR|0o666, 5<<8|2)
				_ = os.Chmod("/dev/ptmx", 0o666)
			}
		}
		n += l.Relabel("/dev", true, map[string]bool{"/dev/pts": true, "/dev/shm": true, "/dev/mqueue": true, tmp: true,
			"/dev/__properties__": true, "/dev/socket": true})
		n += l.Relabel("/", false, nil) // 容器根目录在 Docker 每次新建的那一层，没有标签
	}
	return n, nil
}
