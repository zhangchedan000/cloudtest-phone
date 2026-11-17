package phones

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// 蓝牙（踩坑 #48）。Redroid 自带的蓝牙 HAL 内嵌的芯片模拟器（rootcanal）编译产物是坏的，一开蓝牙就崩。
// 现在的做法（镜像标签 prism.bt=1，安卓 14 起）：
//   - 镜像里换成模拟器的串口蓝牙 HAL，串口是 /dev/ttyS1（host/build-wifi-image.sh）；
//   - 手机里的小助手在 /dev/ttyS1 放一个伪终端，转到 127.0.0.1:6402（sensorsmode.go bridgeBT）；
//   - 管家在这台手机的网络命名空间里起一个自己编的 rootcanal（host/build-rootcanal.sh），听 6402，
//     芯片地址用机型档案里的蓝牙 MAC（PP_BT_ADDR）。每台手机一个，手机之间互相看不到。
// 用 systemd 临时服务跑：管家重启（自动更新）不会把它带走，挂了自动拉起。
// 没有 rootcanal 程序 / 没有 systemd / 镜像没换 HAL：照旧把蓝牙关着（bluetooth_on=0）。

// RootcanalBin 自己编的 rootcanal（测试时换掉）。
var RootcanalBin = "/usr/local/bin/pp-rootcanal"

func btUnit(id string) string { return "ppbt-" + id }

// btAvailable 这台机器能给手机模拟蓝牙芯片吗（镜像支持另看标签）。
func btAvailable() bool {
	if _, err := os.Stat(RootcanalBin); err != nil {
		return false
	}
	_, err := exec.LookPath("systemd-run")
	return err == nil
}

// imageHasBT 镜像换了串口蓝牙 HAL。
func (s *Service) imageHasBT(ctx context.Context, image string) bool {
	return s.imageLabel(ctx, image, "prism.bt") == "1"
}

// rootcanalArgs 在手机网络里跑 rootcanal 的命令（端口都是它的默认值，每台手机自己的网络，互不冲突）。
func rootcanalArgs(pid int) []string {
	return []string{"nsenter", "-t", fmt.Sprint(pid), "-n", RootcanalBin,
		"--test_port=6401", "--hci_port=6402", "--link_port=6403", "--link_ble_port=6404"}
}

// startBT 起这台手机的蓝牙芯片（容器刚启动、拿得到 pid 之后）。失败只记日志：手机照常开机，只是蓝牙开不了。
func (s *Service) startBT(ctx context.Context, id string, byKey map[string]any) {
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	if err != nil || info.State.Pid <= 0 {
		return
	}
	u := btUnit(id)
	_, _ = s.run.Run(ctx, "systemctl", "stop", u)
	_, _ = s.run.Run(ctx, "systemctl", "reset-failed", u)
	args := []string{"--unit=" + u, "--collect", "--quiet", "-p", "Restart=on-failure", "-p", "RestartSec=2",
		"-p", "StandardOutput=null", "--description=prism-phone 手机蓝牙芯片 " + id}
	if mac := strings.TrimSpace(str(byKey["bluetoothMac"])); validMAC(mac) {
		args = append(args, "--setenv=PP_BT_ADDR="+strings.ToLower(mac))
	}
	args = append(args, rootcanalArgs(info.State.Pid)...)
	if out, err := s.run.Run(ctx, "systemd-run", args...); err != nil {
		log.Printf("手机 %s：蓝牙芯片没起来（蓝牙开不了，别的照常）：%v %s", id, err, strings.TrimSpace(out))
	}
}

// stopBT 停这台手机的蓝牙芯片（关机、删除时）。没有就什么都不做。
func (s *Service) stopBT(ctx context.Context, id string) {
	if !btAvailable() {
		return
	}
	_, _ = s.run.Run(ctx, "systemctl", "stop", btUnit(id))
}

// btRunning 这台手机的蓝牙芯片在跑（开机设置据此决定要不要把蓝牙关掉）。
func (s *Service) btRunning(ctx context.Context, id string) bool {
	if !btAvailable() {
		return false
	}
	out, err := s.run.Run(ctx, "systemctl", "is-active", btUnit(id))
	return err == nil && strings.TrimSpace(out) == "active"
}

// validMAC aa:bb:cc:dd:ee:ff。
func validMAC(s string) bool {
	if len(s) != 17 {
		return false
	}
	for i, c := range s {
		if i%3 == 2 {
			if c != ':' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
