package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/zhangchedan000/prism-phone/agent/internal/sensors"
)

// startSensors pp-init 里调用（容器里、安卓 init 之前）：开伪终端绑到 HAL 的管道路径，起喂数据子进程。
// 出错只打印，绝不影响开机。
func startSensors(spec string) {
	master, slave, err := sensors.Setup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pp-init：传感器没配好（手机照常开机，只是没有传感器数据）：%v\n", err)
		return
	}
	cmd := exec.Command("/proc/self/exe", "pp-sensors", spec)
	cmd.ExtraFiles = []*os.File{master, slave} // 子进程里是 3、4
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "pp-init：起传感器进程失败：%v\n", err)
	}
	master.Close()
	slave.Close()
}

// runSensors 喂数据进程（不返回）。
func runSensors(arg string) {
	// 进程名别叫 exe（只有 root 看得见，App 看不到别的进程，但还是起个像样的名字）
	name := []byte("vendor.sensord\x00")
	_, _, _ = syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_NAME, uintptr(unsafe.Pointer(&name[0])), 0)
	spec, err := sensors.Decode(arg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pp-sensors：参数不对：%v\n", err)
		os.Exit(2)
	}
	sensors.Serve(spec, os.NewFile(3, "ptmx"), os.NewFile(4, "pts"), func(string) {})
}
