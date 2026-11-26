package phones

import (
	"context"
	"strings"
	"testing"
)

func TestFakeGPUNodeOnlyAdreno(t *testing.T) {
	r := &fakeRunner{}
	s := &Service{run: r}
	// 高通 Adreno 伪装档：造 /dev/kgsl-3d0（踩坑 #165）。
	s.fakeGPUNode(context.Background(), "abc", &Model{GPU: &GPUInfo{Renderer: "Adreno (TM) 650"}})
	if !strings.Contains(strings.Join(r.calls, "\n"), "mknod /dev/kgsl-3d0") {
		t.Fatalf("高通档应造 kgsl 节点：\n%s", strings.Join(r.calls, "\n"))
	}
	// 非高通（别的 Mali、三星 Xclipse、空、无 GPU）都不造——那些机型不该有 kgsl。
	for _, m := range []*Model{
		{GPU: &GPUInfo{Renderer: "Mali-G720-Immortalis MC12"}},
		{GPU: &GPUInfo{Renderer: "Samsung Xclipse 950"}},
		{GPU: &GPUInfo{Renderer: ""}},
		{},
		nil,
	} {
		r.calls = nil
		s.fakeGPUNode(context.Background(), "abc", m)
		if len(r.calls) != 0 {
			t.Fatalf("非高通 %+v 不该造 kgsl：\n%s", m, strings.Join(r.calls, "\n"))
		}
	}
}

func TestHostDevList(t *testing.T) {
	ls := "__properties__ binder binderfs dri fuse input kmsg loop-control loop0 loop12 loop6p2 sda sda1 sg0 kvm vhost-net vsock vmci zfs tty tty0 tty1 tty63 ttyS0 ttyS1 ttyS3 ttyS4 ttyS31 ttyAMA0 ttyC0 vcs vcsa1 uinput uhid null zero rtc0 rfkill hidraw0 mem full port vhci goldfish_pipe mali0 mpp_service rga"
	got := " " + strings.Join(hostDevList(ls), " ") + " "
	for _, del := range []string{"loop0", "loop12", "loop6p2", "sda", "sda1", "sg0", "kvm", "vhost-net", "vsock", "vmci", "zfs", "tty1", "tty63", "ttyS4", "ttyS31", "ttyAMA0", "vcs", "vcsa1", "hidraw0", "mem", "full", "port", "vhci"} {
		if !strings.Contains(got, " /dev/"+del+" ") {
			t.Fatalf("%s 应该删：%s", del, got)
		}
	}
	// mali0 / mpp_service / rga 保留：硬件编码器每开一路投屏都要 open mpp_service，删了画面编不出来（踩坑 #169 回退 #160）
	for _, keep := range []string{"binder", "binderfs", "dri", "fuse", "input", "kmsg", "loop-control", "tty", "tty0", "ttyS0", "ttyS1", "ttyS3", "ttyC0", "uinput", "uhid", "null", "zero", "rtc0", "rfkill", "__properties__", "goldfish_pipe", "mali0", "mpp_service", "rga"} {
		if strings.Contains(got, " /dev/"+keep+" ") {
			t.Fatalf("%s 不能删", keep)
		}
	}
}

func TestModelMinVersion(t *testing.T) {
	if got := modelMinVersion(&Model{AndroidVersions: []int{16}}); got != 16 {
		t.Fatalf("PKB110 出厂版本应 16，得 %d", got)
	}
	if got := modelMinVersion(&Model{AndroidVersions: []int{14, 15, 16}}); got != 14 {
		t.Fatalf("多版本取最小 14，得 %d", got)
	}
	if got := modelMinVersion(&Model{}); got != 0 {
		t.Fatalf("没录版本返回 0，得 %d", got)
	}
}
