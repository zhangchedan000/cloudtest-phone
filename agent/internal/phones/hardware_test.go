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

func TestPickHardware(t *testing.T) {
	have := []string{"mt6886", "mt6895", "mt6896"}
	cases := []struct {
		vals map[string]any
		want string
	}{
		{map[string]any{"socModel": "MT6896"}, "mt6896"},
		{map[string]any{"socModel": "MT6895Z/TCZA"}, "mt6895"}, // vivo 真机写法带后缀
		{map[string]any{"socModel": "MT6896Z/CZA"}, "mt6896"},
		{map[string]any{"socModel": "SM8550", "boardPlatform": "mt6895"}, "mt6895"},
		{map[string]any{"socModel": "SM8550"}, "mt6886"}, // 都对不上：用镜像里有的，不退回 redroid
	}
	for _, c := range cases {
		if got := pickHardware(have, c.vals); got != c.want {
			t.Errorf("%v → %q，应该是 %q", c.vals, got, c.want)
		}
	}
	if got := pickHardware(nil, map[string]any{"socModel": "MT6895"}); got != "" {
		t.Errorf("老镜像（没有 prism.hw 标签）应该返回空，得到 %q", got)
	}
}
