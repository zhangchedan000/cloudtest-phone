package phones

import (
	"strings"
	"testing"
)

func TestWithHardware(t *testing.T) {
	img := []string{"/init", "androidboot.hardware=redroid", "ro.setupwizard.mode=DISABLED"}
	if got := withHardware(nil, img, "mt6895"); strings.Join(got, " ") != "/init androidboot.hardware=mt6895 ro.setupwizard.mode=DISABLED" {
		t.Fatalf("没启动器时改镜像入口：%v", got)
	}
	ep := append([]string{"/pp-init", "pp-init", "0"}, img...)
	got := withHardware(ep, img, "mt6896")
	if strings.Join(got, " ") != "/pp-init pp-init 0 /init androidboot.hardware=mt6896 ro.setupwizard.mode=DISABLED" {
		t.Fatalf("启动器入口：%v", got)
	}
	if ep[4] != "androidboot.hardware=redroid" {
		t.Fatal("不能改到原切片")
	}
	if got := withHardware(nil, []string{"/init"}, "mt6895"); got != nil {
		t.Fatalf("入口里没有 hardware 参数就不动：%v", got)
	}
}

func TestDropRedroidLines(t *testing.T) {
	in := "# from device/redroid/redroid.prop\nro.hardware=mt6895\nro.boot.use_redroid_c2=1\nro.vendor.x=1\n"
	if got := dropRedroidLines(in); got != "ro.hardware=mt6895\nro.vendor.x=1\n" {
		t.Fatalf("%q", got)
	}
}
