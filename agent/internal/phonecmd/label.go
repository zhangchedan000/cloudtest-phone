package phonecmd

import (
	"bufio"
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
		if ctx := l.Lookup(p, fi.Mode()); ctx != "" && lsetxattr(p, ctx) == nil {
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

// 内核自己的文件系统（标签由规则里的 genfscon 定）：这些挂载不碰
var kernelFS = map[string]bool{"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true, "tmpfs": true, "devpts": true,
	"mqueue": true, "selinuxfs": true, "binder": true, "debugfs": true, "tracefs": true, "bpf": true, "pstore": true,
	"configfs": true, "fusectl": true, "securityfs": true, "functionfs": true, "overlay": true}

// LabelBoot 开机前的补标签：宿主机挂进来的文件 / 目录（Docker bind，文件系统是宿主机的盘）+ /dev 整个。
// 挂在 /proc、/sys 下面的伪造文件用新挂一份 proc / sysfs 里同一路径的标签（genfscon 定的，比如 proc_cpuinfo）。
func LabelBoot() (int, error) {
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
	// 伪造文件：照真文件（新挂的 proc / sysfs 里同一路径，没有就往上找父目录）的标签
	fromKernel := func(mp, root string) int {
		n := 0
		_ = filepath.Walk(mp, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			ref := root + p[len(root[len(tmp):]):]
			ctx := ""
			for q := ref; len(q) > len(tmp) && ctx == ""; q = filepath.Dir(q) {
				ctx = lgetxattr(q)
			}
			if ctx != "" && lsetxattr(p, ctx) == nil {
				n++
			}
			return nil
		})
		return n
	}
	n := 0
	for _, m := range mounts() {
		mp := m.point
		if kernelFS[m.fstype] || mp == "/" || mp == "/data" || strings.HasPrefix(mp, "/data/.pp") || strings.HasPrefix(mp, tmp) {
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
		}
	}
	n += l.Relabel("/dev", true, map[string]bool{"/dev/pts": true, "/dev/shm": true, "/dev/mqueue": true, tmp: true})
	return n, nil
}
