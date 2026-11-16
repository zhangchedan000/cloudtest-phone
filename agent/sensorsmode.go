package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/modem"
	"github.com/zhangchedan000/prism-phone/agent/internal/props"
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
	var dev *touch.Device
	if spec.W > 0 && spec.H > 0 {
		d, err := touch.Create(touch.Config{W: spec.W, H: spec.H, Name: spec.TouchName}, "/dev/input", spec.Seed)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vendor.sensord：触摸屏没建起来：%v\n", err)
		} else {
			dev = d
			go dev.KeepNode()
		}
	}
	// 基带和 SIM 卡（踩坑 #65）：rild 连 /dev/ttyC0，这里扮演基带
	if spec.Modem != nil {
		if master, slave, err := sensors.SetupModem(); err != nil {
			fmt.Fprintf(os.Stderr, "vendor.sensord：基带没配好：%v\n", err)
		} else {
			go func() {
				defer slave.Close() // 一直开着：rild 重启时主端读写不会出错
				m := modem.New(*spec.Modem, master)
				for {
					_ = m.Serve(master)
					time.Sleep(time.Second)
				}
			}()
		}
	}
	// 模拟器配套的电话驱动（libcuttlefish-ril-2）用 -p 端口连本机 TCP（每台手机自己的网络命名空间，端口不冲突）
	if spec.Modem != nil {
		go serveModemTCP(*spec.Modem)
	}
	// 管家的命令通道：触摸 + 系统属性（开机后藏 ro.boot.redroid_* 等，踩坑 #63）
	go func() { _ = touch.Serve(func(cmd string) error { return helperCommand(dev, cmd) }, func(string) {}) }()
	sensors.Serve(spec, os.NewFile(3, "ptmx"), os.NewFile(4, "pts"), func(string) {})
}

// helperCommand 小助手执行一条管家的命令。
func helperCommand(dev *touch.Device, cmd string) error {
	f := strings.Fields(cmd)
	if len(f) >= 3 && f[0] == "prop" {
		switch {
		case f[1] == "hide" && len(f) == 3:
			_, err := props.Hide(f[2])
			return err
		case f[1] == "set" && len(f) >= 3:
			ok, err := props.Set(f[2], strings.Join(f[3:], " "))
			if err == nil && !ok {
				err = fmt.Errorf("没有属性 %s", f[2])
			}
			return err
		}
		return fmt.Errorf("不认识的命令：%q", cmd)
	}
	if dev == nil {
		return fmt.Errorf("没有触摸屏")
	}
	return dev.Do(cmd)
}

// ModemPort 模拟器配套电话驱动连的本机端口（镜像里 init.radio.rc 配的 -p 9600）。
const ModemPort = "127.0.0.1:9600"

func serveModemTCP(spec modem.Spec) {
	for {
		ln, err := net.Listen("tcp", ModemPort)
		if err != nil {
			time.Sleep(2 * time.Second) // 网卡还没起来等情况：过会儿再试
			continue
		}
		for {
			c, err := ln.Accept()
			if err != nil {
				break
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = modem.New(spec, c).Serve(c)
			}(c)
		}
		ln.Close()
	}
}
