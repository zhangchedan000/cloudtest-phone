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
// 用系统的守护工具跑：Ubuntu 用 systemd 临时服务（systemd-run），Alpine 板子（OpenRC，没有 systemd）用
// supervise-daemon。管家重启（自动更新）不会把它带走，挂了自动拉起。
// 没有 rootcanal 程序 / 两种守护工具都没有 / 镜像没换 HAL：照旧把蓝牙关着（bluetooth_on=0）。
// 以前只认 systemd-run：板子上从来没起过 rootcanal，开机时蓝牙 HAL 打不开 /dev/ttyS1、
// com.android.bluetooth 连崩 4~6 次才放弃（踩坑 #122）。

// RootcanalBin 自己编的 rootcanal（测试时换掉）。
var RootcanalBin = "/usr/local/bin/pp-rootcanal"

func btUnit(id string) string { return "ppbt-" + id }

// lookPath 找命令（测试时换掉）。
var lookPath = exec.LookPath

// btLauncher 用哪种守护工具跑 rootcanal："systemd" / "openrc" / ""（都没有）。
func btLauncher() string {
	if _, err := lookPath("systemd-run"); err == nil {
		return "systemd"
	}
	if _, err := lookPath("supervise-daemon"); err == nil {
		return "openrc"
	}
	return ""
}

// btAvailable 这台机器能给手机模拟蓝牙芯片吗（镜像支持另看标签）。
func btAvailable() bool {
	if _, err := os.Stat(RootcanalBin); err != nil {
		return false
	}
	return btLauncher() != ""
}

// btPidfile OpenRC supervise-daemon 用的 pid 文件。
func btPidfile(id string) string { return "/run/" + btUnit(id) + ".pid" }

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
	mac := strings.TrimSpace(str(byKey["bluetoothMac"]))
	if btLauncher() == "openrc" {
		// supervise-daemon <名字> --start ... <命令> -- <参数>（和板子上 mihomo 的 init 脚本同一种用法）
		_, _ = s.run.Run(ctx, "env", "RC_SVCNAME="+u, "supervise-daemon", u, "--stop", "--pidfile", btPidfile(id))
		// RC_SVCNAME：supervise-daemon 要求第一个参数等于当前服务名，管家自己跑在 prism-phone-agent 服务里、
		// 会把这个变量继承下来（实测报「the first argument is ppbt-… and must be prism-phone-agent」）
		args := []string{"RC_SVCNAME=" + u, "supervise-daemon", u, "--start", "--pidfile", btPidfile(id), "--respawn-delay", "2", "--respawn-max", "0",
			"--stdout", "/dev/null", "--stderr", "/dev/null"}
		if validMAC(mac) {
			args = append(args, "--env", "PP_BT_ADDR="+strings.ToLower(mac))
		}
		rc := rootcanalArgs(info.State.Pid)
		args = append(append(args, rc[0], "--"), rc[1:]...)
		if out, err := s.run.Run(ctx, "env", args...); err != nil {
			log.Printf("手机 %s：蓝牙芯片没起来（蓝牙开不了，别的照常）：%v %s", id, err, strings.TrimSpace(out))
		}
		return
	}
	_, _ = s.run.Run(ctx, "systemctl", "stop", u)
	_, _ = s.run.Run(ctx, "systemctl", "reset-failed", u)
	args := []string{"--unit=" + u, "--collect", "--quiet", "-p", "Restart=on-failure", "-p", "RestartSec=2",
		"-p", "StandardOutput=null", "--description=prism-phone 手机蓝牙芯片 " + id}
	if validMAC(mac) {
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
	if btLauncher() == "openrc" {
		_, _ = s.run.Run(ctx, "env", "RC_SVCNAME="+btUnit(id), "supervise-daemon", btUnit(id), "--stop", "--pidfile", btPidfile(id))
		return
	}
	_, _ = s.run.Run(ctx, "systemctl", "stop", btUnit(id))
}

// btRunning 这台手机的蓝牙芯片在跑（开机设置据此决定要不要把蓝牙关掉）。
func (s *Service) btRunning(ctx context.Context, id string) bool {
	if !btAvailable() {
		return false
	}
	if btLauncher() == "openrc" {
		// pid 文件里是守护进程（supervise-daemon）的 pid：它在就算在跑（rootcanal 挂了它会拉起）
		b, err := os.ReadFile(btPidfile(id))
		if err != nil {
			return false
		}
		_, err = os.Stat("/proc/" + strings.TrimSpace(string(b)))
		return err == nil
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
