package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/zhangchedan000/prism-phone/agent/internal/sensors"
	"github.com/zhangchedan000/prism-phone/agent/internal/touch"
)

// startSensors pp-init 里调用（容器里、安卓 init 之前）：开伪终端绑到 HAL 的管道路径，起喂数据子进程。
// 出错只打印，绝不影响开机。
func startSensors(spec string) {
	master, slave, err := sensors.Setup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pp-init：传感器没配好（手机照常开机，只是没有传感器数据）：%v\n", err)
		return
	}
	// 配置走管道（fd 5），不放命令行：手机里 ps 看到的只是 vendor.sensord
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pp-init：传感器没配好：%v\n", err)
		return
	}
	cmd := &exec.Cmd{Path: "/proc/self/exe", Args: []string{"vendor.sensord"}, Env: []string{"PP_MODE=pp-sensors"}}
	cmd.ExtraFiles = []*os.File{master, slave, r} // 子进程里是 3、4、5
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "pp-init：起传感器进程失败：%v\n", err)
	}
	_, _ = w.WriteString(spec)
	w.Close()
	r.Close()
	master.Close()
	slave.Close()
}

// runSensors 喂数据进程（不返回）：配置从 fd 5 读。
func runSensors() {
	arg, _ := io.ReadAll(os.NewFile(5, "spec"))
	// 进程名别叫 exe（只有 root 看得见，App 看不到别的进程，但还是起个像样的名字）
	// /proc/self/comm 是整个进程（主线程）的名字；prctl 只改当前线程，Go 的 main 不一定在主线程上
	_ = os.WriteFile("/proc/self/comm", []byte("vendor.sensord"), 0)
	os.Unsetenv("PP_MODE")
	spec, err := sensors.Decode(string(arg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "pp-sensors：参数不对：%v\n", err)
		os.Exit(2)
	}
	// 虚拟触摸屏（踩坑 #61）：建不起来不影响传感器，管家那边会退回 input 命令
	if spec.W > 0 && spec.H > 0 {
		if dev, err := touch.Create(touch.Config{W: spec.W, H: spec.H, Name: spec.TouchName}, "/dev/input", spec.Seed); err != nil {
			fmt.Fprintf(os.Stderr, "vendor.sensord：触摸屏没建起来：%v\n", err)
		} else {
			go dev.KeepNode()
			go func() { _ = touch.Serve(dev, func(string) {}) }()
		}
	}
	sensors.Serve(spec, os.NewFile(3, "ptmx"), os.NewFile(4, "pts"), func(string) {})
}
