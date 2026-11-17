package phones

import (
	"strings"
	"testing"
)

func TestHostDevList(t *testing.T) {
	ls := "__properties__ binder binderfs dri fuse input kmsg loop-control loop0 loop12 loop6p2 sda sda1 sg0 kvm vhost-net vsock vmci zfs tty tty0 tty1 tty63 ttyS0 ttyS1 ttyS3 ttyS4 ttyS31 ttyAMA0 ttyC0 vcs vcsa1 uinput uhid null zero rtc0 rfkill hidraw0 mem full port vhci goldfish_pipe"
	got := " " + strings.Join(hostDevList(ls), " ") + " "
	for _, del := range []string{"loop0", "loop12", "loop6p2", "sda", "sda1", "sg0", "kvm", "vhost-net", "vsock", "vmci", "zfs", "tty1", "tty63", "ttyS4", "ttyS31", "ttyAMA0", "vcs", "vcsa1", "hidraw0", "mem", "full", "port", "vhci"} {
		if !strings.Contains(got, " /dev/"+del+" ") {
			t.Fatalf("%s 应该删：%s", del, got)
		}
	}
	for _, keep := range []string{"binder", "binderfs", "dri", "fuse", "input", "kmsg", "loop-control", "tty", "tty0", "ttyS0", "ttyS1", "ttyS3", "ttyC0", "uinput", "uhid", "null", "zero", "rtc0", "rfkill", "__properties__", "goldfish_pipe"} {
		if strings.Contains(got, " /dev/"+keep+" ") {
			t.Fatalf("%s 不能删", keep)
		}
	}
}
