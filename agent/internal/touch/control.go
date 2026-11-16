package touch

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SocketName 小助手在手机网络命名空间里监听的抽象 unix socket（不落文件；App 读不到 /proc/net/unix）。
const SocketName = "@vendor.sensord"

// Serve 接收管家发来的命令（一行一条，做完回 ok），交给 handle：
//
//	tap X Y / swipe X1 Y1 X2 Y2 毫秒（触摸屏）
//	prop hide 名字 / prop set 名字 值（系统属性，见 internal/props）
func Serve(handle func(cmd string) error, logf func(string)) error {
	ln, err := net.Listen("unix", SocketName)
	if err != nil {
		return err
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(10 * time.Second))
			line, err := bufio.NewReader(c).ReadString('\n')
			if err != nil {
				return
			}
			if err := handle(strings.TrimSpace(line)); err != nil {
				logf(err.Error())
				fmt.Fprintf(c, "err %v\n", err)
				return
			}
			fmt.Fprint(c, "ok\n")
		}(c)
	}
}

// Do 执行一条命令。
func (d *Device) Do(cmd string) error {
	f := strings.Fields(cmd)
	n := make([]int, len(f))
	for i := 1; i < len(f); i++ {
		v, err := strconv.Atoi(f[i])
		if err != nil {
			return fmt.Errorf("参数不对：%q", cmd)
		}
		n[i] = v
	}
	switch {
	case len(f) == 3 && f[0] == "tap":
		return d.Tap(n[1], n[2])
	case len(f) == 6 && f[0] == "swipe":
		return d.Swipe(n[1], n[2], n[3], n[4], n[5])
	}
	return fmt.Errorf("不认识的命令：%q", cmd)
}

// Send 管家这边：进手机的网络命名空间（/proc/<容器进程>/ns/net）连小助手，发一条命令，等它做完。
func Send(netnsPath, cmd string, timeout time.Duration) error {
	ch := make(chan error, 1)
	go func() {
		// 网络命名空间按线程算：锁住线程切进去，用完不切回（goroutine 结束时 Go 会丢掉这个线程）
		runtime.LockOSThread()
		fd, err := os.Open(netnsPath)
		if err != nil {
			ch <- err
			return
		}
		defer fd.Close()
		if _, _, e := syscall.RawSyscall(sysSetns, fd.Fd(), syscall.CLONE_NEWNET, 0); e != 0 {
			ch <- fmt.Errorf("进入手机网络命名空间：%v", e)
			return
		}
		c, err := net.DialTimeout("unix", SocketName, 2*time.Second)
		if err != nil {
			ch <- err
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(timeout))
		if _, err := fmt.Fprintf(c, "%s\n", cmd); err != nil {
			ch <- err
			return
		}
		reply, err := bufio.NewReader(c).ReadString('\n')
		if err != nil {
			ch <- err
			return
		}
		if reply = strings.TrimSpace(reply); reply != "ok" {
			ch <- fmt.Errorf("小助手：%s", reply)
			return
		}
		ch <- nil
	}()
	return <-ch
}
