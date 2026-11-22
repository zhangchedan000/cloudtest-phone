//go:build linux && arm64

package timens

import (
	_ "embed"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// helper 单线程小启动器 pp-timens（host/timens/pp-timens.c，sh host/timens/build.sh 编出来的，踩坑 #132）。
//
//go:embed pp-timens-arm64
var helper []byte

const (
	sysMemfdCreate = 279 // arm64
	mfdCloexec     = 1
)

// execHelper 把 pp-timens 放进一块匿名内存文件（容器里不留文件），exec 它：它在单线程里建时间命名空间、
// setns 自己进去，再 exec 安卓 init。只在出错时返回。
func execHelper(args []string) error {
	name, _ := syscall.BytePtrFromString("pp-timens")
	fd, _, e := syscall.Syscall(sysMemfdCreate, uintptr(unsafe.Pointer(name)), mfdCloexec, 0)
	if e != 0 {
		return fmt.Errorf("memfd_create：%w", e)
	}
	f := os.NewFile(fd, "pp-timens")
	if _, err := f.Write(helper); err != nil {
		return err
	}
	// MFD_CLOEXEC：exec 读进程序后这个 fd 就关了，安卓 init 不会继承它
	return syscall.Exec(fmt.Sprintf("/proc/self/fd/%d", fd), append([]string{"pp-timens"}, args...), os.Environ())
}
