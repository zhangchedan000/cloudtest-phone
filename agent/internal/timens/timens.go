// Package timens 让云手机看到的「开机时长」从 0 开始（踩坑 #49）。
//
// 容器和宿主机共用一个内核，安卓里 /proc/uptime、SystemClock.elapsedRealtime()（CLOCK_BOOTTIME）、
// SystemClock.uptimeMillis()（CLOCK_MONOTONIC）读到的都是宿主机开了多久（实测 100 多天）。
// 只改 /proc/uptime 文件会和 elapsedRealtime 对不上，反而更假。
// Linux 5.6 起有「时间命名空间」：给新命名空间里的 CLOCK_MONOTONIC / CLOCK_BOOTTIME 加一个偏移，
// 里面所有进程（包括 vdso、/proc/uptime、sysinfo）看到的都一致地从这个值开始走。
//
// 用法：管家程序自己当启动器（静态编译，安卓容器里也能跑）——容器入口改成
//
//	/pp-init pp-init <开机时长秒数> /init <镜像原来的参数...>
//
// 它先建时间命名空间、设好偏移，再 exec 安卓的 init（exec 时进程进入新的时间命名空间，
// init 和它所有子进程都在里面）。
package timens

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"
)

const (
	cloneNewTime   = 0x80 // CLONE_NEWTIME
	clockMonotonic = 1
	clockBoottime  = 7
)

// Supported 宿主机内核支持时间命名空间吗。
func Supported() bool {
	_, err := os.Stat("/proc/self/ns/time")
	return err == nil
}

func now(clock uintptr) int64 {
	var ts syscall.Timespec
	_, _, e := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, clock, uintptr(unsafe.Pointer(&ts)), 0)
	if e != 0 {
		return 0
	}
	return int64(ts.Sec)
}

// Offsets 让两个时钟在命名空间里都从 uptime 秒开始：偏移 = 想要的值 - 现在的值（整秒，结果不会为负）。
func Offsets(uptime int64, monoNow, bootNow int64) string {
	return fmt.Sprintf("monotonic %d 0\nboottime %d 0\n", uptime-monoNow, uptime-bootNow)
}

// Exec 不建时间命名空间，直接 exec（不返回）：args = <开机时长秒数（忽略）> <程序> <参数...>。
// 板子内核 CLONE_NEWTIME 有问题（exec 后建线程 EINVAL，踩坑 #98）时用：pp-init 照常喂传感器/RIL，
// 只是开机时长还是宿主机的（等 pp_mnt 内核模块的 uptime 钩子补）。
func Exec(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "用法：pp-init --no-timens <开机时长秒数> <程序> [参数...]")
		os.Exit(2)
	}
	err := syscall.Exec(args[1], args[1:], os.Environ())
	fmt.Fprintf(os.Stderr, "pp-init：启动 %s 失败：%v\n", args[1], err)
	os.Exit(1)
}

// Run 启动器入口（不返回）：args = <开机时长秒数> <程序> <参数...>。
// 建命名空间失败也照样启动安卓（只是开机时长还是宿主机的），绝不因为这个开不了机。
func Run(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "用法：pp-init <开机时长秒数> <程序> [参数...]")
		os.Exit(2)
	}
	// unshare / 写偏移 / exec 必须在同一个系统线程上（命名空间是按线程记的）。
	runtime.LockOSThread()
	uptime, _ := strconv.ParseInt(args[0], 10, 64)
	if uptime < 0 {
		uptime = 0
	}
	if err := syscall.Unshare(cloneNewTime); err != nil {
		fmt.Fprintf(os.Stderr, "pp-init：建时间命名空间失败（开机时长保持宿主机的）：%v\n", err)
	} else {
		off := Offsets(uptime, now(clockMonotonic), now(clockBoottime))
		if err := os.WriteFile("/proc/self/timens_offsets", []byte(off), 0); err != nil {
			fmt.Fprintf(os.Stderr, "pp-init：设时钟偏移失败：%v\n", err)
		}
	}
	err := syscall.Exec(args[1], args[1:], os.Environ())
	fmt.Fprintf(os.Stderr, "pp-init：启动 %s 失败：%v\n", args[1], err)
	os.Exit(1)
}
