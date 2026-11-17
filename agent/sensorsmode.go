package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/h4"
	"github.com/zhangchedan000/prism-phone/agent/internal/modem"
	"github.com/zhangchedan000/prism-phone/agent/internal/props"
	"github.com/zhangchedan000/prism-phone/agent/internal/sensors"
	"github.com/zhangchedan000/prism-phone/agent/internal/touch"
)

// startSensors pp-init 里调用（容器里、安卓 init 之前）：开伪终端绑到 HAL 的管道路径，起喂数据子进程。
// 出错只打印，绝不影响开机。
func startSensors(spec string) {
	paths := sensors.PipePaths
	if sp, err := sensors.Decode(spec); err == nil {
		if sp.RealNames {
			paths = sensors.RealPipePaths // 新镜像：传感器驱动开的是真机的设备名（踩坑 #75）
		}
		if sp.Battery != nil { // 电池的 sysfs（踩坑 #78）
			if err := sensors.SetupPowerSupply(*sp.Battery); err != nil {
				fmt.Fprintf(os.Stderr, "pp-init：电池目录没造好（照常开机）：%v\n", err)
			}
		}
	}
	master, slave, err := sensors.SetupPaths(paths)
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
	// 蓝牙（踩坑 #48）：HAL 的串口 ↔ rootcanal 的 HCI 端口，都是 H4，原样转
	if spec.BT {
		if master, slave, err := sensors.SetupBT(); err != nil {
			fmt.Fprintf(os.Stderr, "vendor.sensord：蓝牙串口没配好：%v\n", err)
		} else {
			go bridgeBT(master, slave)
		}
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

// BTPort 这台手机网络里 rootcanal 的 HCI 端口（H4 over TCP，管家起的，host/build-rootcanal.sh）。
const BTPort = "127.0.0.1:6402"

// bridgeBT 蓝牙 HAL 的串口（伪终端主端）和 rootcanal 之间转发 H4 包。rootcanal 断了就重连
// （它每来一个连接新建一块芯片，安卓的蓝牙会超时后自己重新初始化）。没连上时 HAL 发的数据丢掉。
// 按整包写串口；HAL 每次初始化发 HCI_Reset 时先清掉串口里没读走的旧包（蓝牙关了再开，
// 上一轮芯片发的事件还留在串口里，HAL 读到会对不上、初始化超时崩溃，踩坑 #48）。
func bridgeBT(master, slave *os.File) {
	defer slave.Close() // 一直开着：HAL 重启时主端读写不会出错
	var mu sync.Mutex   // 管 cur
	var wmu sync.Mutex  // 写串口和清串口不能交错
	var cur net.Conn
	go func() { // 只有这一个 goroutine 读串口（HAL → 芯片）
		buf := make([]byte, 4096)
		var rest []byte
		for {
			n, err := master.Read(buf)
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			rest = h4.Split(append(rest, buf[:n]...), func(pkt []byte) {
				if h4.IsReset(pkt) {
					wmu.Lock()
					sensors.FlushInput(slave)
					wmu.Unlock()
				}
				mu.Lock()
				c := cur
				mu.Unlock()
				if c != nil {
					_, _ = c.Write(pkt)
				}
			})
		}
	}()
	for {
		c, err := net.DialTimeout("tcp", BTPort, 2*time.Second)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		mu.Lock()
		cur = c
		mu.Unlock()
		buf := make([]byte, 4096)
		var rest []byte
		for { // 芯片 → HAL：拆成整包再写
			n, err := c.Read(buf)
			if n > 0 {
				rest = h4.Split(append(rest, buf[:n]...), func(pkt []byte) {
					wmu.Lock()
					_, _ = master.Write(pkt)
					wmu.Unlock()
				})
			}
			if err != nil {
				break
			}
		}
		mu.Lock()
		cur = nil
		mu.Unlock()
		c.Close()
		time.Sleep(500 * time.Millisecond)
	}
}
