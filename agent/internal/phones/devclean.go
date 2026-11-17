package phones

import (
	"context"
	"log"
	"regexp"
	"strings"
)

// 手机 /dev 里服务器的硬件（2026-11-17 全面体检发现，踩坑 #74）：容器是特权的，Docker 把宿主机 /dev 下的设备节点都给了它——
// 服务器的硬盘 sda、80 个 loop、kvm / vhost / vfio / vmci / vsock（虚拟化）、zfs、btrfs、64 个 tty、32 个 ttyS、vcs……
// 真手机一个都没有，App 列 /dev 就看见了。/dev 是这台容器自己的 tmpfs，删节点不影响宿主机。
// 开机完成后删（安卓开机过程用不到这些；实测删 229 个后屏幕、WiFi、电话、蓝牙开关、传感器、触摸、开 App 都正常）。
// 留下的：binder 系列、dri、dma_heap、input、uinput、uhid、fuse、snd、net/tun、ppp、rtc0、hwrng、rfkill、ttyS0~3（ttyS1 是蓝牙）、
// ttyC0（基带）、tty、kmsg 等安卓要用的，以及我们自己的串口。
var hostDevNodes = regexp.MustCompile(`^(loop[0-9]+(p[0-9]+)?|sd[a-z]+[0-9]*|sg[0-9]+|bsg|nvme.*|vfio|vhost-net|vhost-vsock|vsock|vmci|kvm|xen.*|zfs|btrfs-control|mapper|dm-user|ecryptfs|cuse|autofs|userio|psaux|vga_arbiter|ttyprintk|port|mem|kmem|core|full|ttyAMA[0-9]+|ttyS([4-9]|[1-9][0-9])|tty[1-9][0-9]*|vcs.*|hidraw[0-9]+|kmsg_debug|nvram|hpet|fd0|lp[0-9]+|parport[0-9]+|watchdog[0-9]*|tpm.*|mei[0-9]*|sev.*|udmabuf|vhci)$`)

// hostDevList 从 ls /dev 的输出里挑出要删的。
func hostDevList(ls string) []string {
	var out []string
	for _, f := range strings.Fields(ls) {
		if hostDevNodes.MatchString(f) {
			out = append(out, "/dev/"+f)
		}
	}
	return out
}

// cleanDev 删掉这台手机 /dev 里服务器硬件的节点（开机后设置里调用，失败只记日志）。
func (s *Service) cleanDev(ctx context.Context, id string) {
	name := containerName(id)
	out, err := s.run.Run(ctx, "docker", "exec", name, "ls", "/dev")
	if err != nil {
		return
	}
	list := hostDevList(out)
	if len(list) == 0 {
		return
	}
	if o, err := s.run.Run(ctx, "docker", append([]string{"exec", name, "rm", "-rf"}, list...)...); err != nil {
		log.Printf("手机 %s：/dev 里服务器设备没删干净：%v %s", id, err, strings.TrimSpace(o))
	}
}
