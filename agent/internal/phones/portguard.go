package phones

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// 手机里给自己用的本机端口不让 App 连（2026-11-17）：
//   - 9600：模拟基带（电话驱动 rild 以 radio 身份连，sensorsmode.go serveModemTCP）；
//   - 6401~6404：蓝牙芯片模拟器 rootcanal（小助手以 root 连 6402，bluetooth.go）。
// 真手机上没有这些端口，App 扫本机端口或者直接连上去说 AT 命令 / HCI 就露馅了。
// 做法：在这台手机的网络命名空间里加一条防火墙规则——App（uid ≥ 10000，含隔离进程）连这几个端口直接拒绝（回 RST，
// 和端口没开一样）；系统进程（rild、小助手）照常。用宿主机的 iptables（nft 后端），和安卓自己 netd 管的规则互不干扰。
// 还剩：/proc/net/tcp 里能看到这几个端口在监听（这台没开 SELinux，App 能读）——要到有 SELinux 的板子上才能挡。

var guardedPorts = "6401:6404,9600"

// portGuardRule iptables 规则（不带 -I / -C）。
func portGuardRule() []string {
	return []string{"OUTPUT", "-o", "lo", "-p", "tcp", "-m", "multiport", "--dports", guardedPorts,
		"-m", "owner", "--uid-owner", "10000-2147483647", "-j", "REJECT", "--reject-with", "tcp-reset"}
}

// guardLocalPorts 给这台手机加规则（已经有就不重复加）。失败只记日志。
func (s *Service) guardLocalPorts(ctx context.Context, id string) {
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	if err != nil || info.State.Pid <= 0 {
		return
	}
	in := []string{"-t", fmt.Sprint(info.State.Pid), "-n", "iptables", "-w"}
	check := append(append(append([]string{}, in...), "-C"), portGuardRule()...)
	if _, err := s.run.Run(ctx, "nsenter", check...); err == nil {
		return
	}
	add := append(append(append([]string{}, in...), "-I"), portGuardRule()...)
	if out, err := s.run.Run(ctx, "nsenter", add...); err != nil {
		log.Printf("手机 %s：本机端口没挡上（App 能连 %s）：%v %s", id, guardedPorts, err, strings.TrimSpace(out))
	}
}
