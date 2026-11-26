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
// mpp_service / rga 是瑞芯微（RK3588 板子）专有的硬件节点（视频编解码 MPP、2D 加速 RGA）。
// ⚠️ 原来 #160 把它们也删了（想消掉「联发科机型却有瑞芯微节点」的破绽），但这是错的，已回退（踩坑 #169）：
//   - 实时画面的 screenrecord 用硬件 H.264 编码，MPP 编码器**每开一路都重新 open /dev/mpp_service**（不是开机开一次就留着 fd）。
//     节点被删 → 每次新投屏 mpp_init 失败（err -1）→ 一帧都编不出来 → 网页端退回「每秒一张截图」→ 画面巨卡。
//     #160 当初「验证安全」只测了 widevine（走 binder、不碰 /dev）和探针扫描，**从没验过一路全新的硬件编码**，漏了这个回归。
//   - 竞品 dobox 在同款 RK3588 上也**保留**这俩节点（它是非特权容器，只透传需要的设备），靠 SELinux + hidemap 藏自己的软件痕迹，从不删 mpp_service。
//   - 残留的「stat /dev/mpp_service 能认出瑞芯微」是很深很偏的检测（EACCES/存在性），绝大多数检测不查；要进一步隐藏按 dobox 思路（非特权化 / 改标签）另做，别靠删编码器要用的硬件节点。
// mali0 同理保留——联发科 Mali 机型本来就该有 /dev/mali0。
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

// fakeGPUNode 给声称高通 Adreno 的伪装档补一个 /dev/kgsl-3d0 空壳节点（开机后设置里调用，踩坑 #165）。
// 板子真 GPU 是 Mali（设备节点 /dev/mali0），高通真机是 Adreno（/dev/kgsl-3d0）。App 不能 ls /dev（SELinux 挡），
// 但能 stat 具体路径——「ro.hardware=qcom 却有 /dev/mali0、没有 /dev/kgsl-3d0」是常见识别点（我们自己的探针就查这个）。
// mali0 删不掉（每个要画界面的 App 自己都要 open 它做 GPU 渲染，删了就黑屏），所以这步只补上 kgsl 节点、
// 消掉「有 mali 没 kgsl」的组合破绽。复用 mali0 的主次设备号（指向真 Mali 驱动，App open 也能成功、更像），
// 打 gpu_device 标签让 App 能访问。深层（kgsl 的 ioctl 行为、Adreno 专属 GL 扩展、跑分）仍是 Mali，属跨架构伪装的天花板。
func (s *Service) fakeGPUNode(ctx context.Context, id string, m *Model) {
	if m == nil || m.GPU == nil || !strings.Contains(strings.ToLower(m.GPU.Renderer), "adreno") {
		return
	}
	name := containerName(id)
	// mali0 在、kgsl 还没有才造；用 mali0 的设备号 mknod，chmod 666 + 打 gpu_device 标签。
	const script = `[ -e /dev/mali0 ] || exit 0
[ -e /dev/kgsl-3d0 ] && exit 0
set -- $(ls -l /dev/mali0); maj=${5%,}; min=$6
case "$maj$min" in ""|*[!0-9]*) exit 0;; esac
mknod /dev/kgsl-3d0 c "$maj" "$min" || exit 0
chmod 666 /dev/kgsl-3d0
chcon u:object_r:gpu_device:s0 /dev/kgsl-3d0 2>/dev/null`
	if out, err := s.run.Run(ctx, "docker", "exec", name, "sh", "-c", script); err != nil {
		log.Printf("手机 %s：造 /dev/kgsl-3d0 失败：%v %s", id, err, strings.TrimSpace(out))
	}
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
