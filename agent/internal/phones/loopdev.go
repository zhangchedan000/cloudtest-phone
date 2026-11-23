package phones

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// 宿主机预先建好空闲 loop 设备（2026-11-23 多开实测）：安卓 16 每台开机要挂三十来个 APEX，每个占一个 loop 设备。
// 宿主机现成的空闲 loop 设备用完以后，手机里的 apexd 会让内核新建一个（LOOP_CTL_GET_FREE），内核在宿主机的 /dev
// 里建了节点，但已经跑起来的容器的 /dev 是 Docker 开机时照宿主机建的，没有这个新节点 → apexd 一直等「Loopback device
// N not ready」→ APEX 挂不上 → zygote 找不到类、反复崩溃，开不了机。几台一起开机最容易碰上（loop 设备是整台机器共用的）。
// 做法：管家启动时、每台手机开机前，保证宿主机至少有 loopIdleWant 个空闲 loop 设备（内核注册了、没绑文件），
// 不够就用 LOOP_CTL_ADD 补；容器开机时 Docker 照宿主机 /dev 给特权容器建节点，apexd 直接用现成的。
// 空闲的 loop 设备几乎不占资源。管家每次启动 / 开机前都会补，板子重启也不用另外配。

const (
	loopIdleWant = 320  // 8 台安卓 16 同时开机，每台三十来个 APEX + 存储镜像
	loopMax      = 1024 // 最多建到 loop1023
	loopCtlAdd   = 0x4C80
)

var loopMu sync.Mutex

// ensureLoopDevices 保证宿主机空闲 loop 设备够用。失败只记日志（不挡开机）。
func ensureLoopDevices() {
	loopMu.Lock()
	defer loopMu.Unlock()
	idle, top := 0, -1
	entries, _ := filepath.Glob("/sys/block/loop*")
	for _, e := range entries {
		n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(e), "loop"))
		if err != nil {
			continue
		}
		if n > top {
			top = n
		}
		if _, err := os.Stat(filepath.Join(e, "loop", "backing_file")); os.IsNotExist(err) {
			idle++
		}
	}
	if idle >= loopIdleWant {
		return
	}
	f, err := os.OpenFile("/dev/loop-control", os.O_RDWR, 0)
	if err != nil {
		log.Printf("预建 loop 设备：打不开 /dev/loop-control：%v", err)
		return
	}
	defer f.Close()
	added := 0
	for i := top + 1; idle < loopIdleWant && i < loopMax; i++ {
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), loopCtlAdd, uintptr(i)); e == 0 {
			idle++
			added++
		}
	}
	if added > 0 {
		log.Printf("预建 loop 设备 %d 个（空闲 %d 个，安卓开机挂 APEX 用）", added, idle)
	}
}

// PreallocLoopDevices 管家启动时调一次（板子刚开机时宿主机只有内核默认的几十个）。
func PreallocLoopDevices() { ensureLoopDevices() }
