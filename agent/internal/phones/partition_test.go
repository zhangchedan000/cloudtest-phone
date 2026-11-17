package phones

import (
	"strings"
	"testing"
)

func TestPartitionIdentity(t *testing.T) {
	args := []string{
		"ro.system.build.fingerprint=Android/missi_phone_global/missi:14/UP1A.230905.011/V816.0.12.0.ULQMIXM:user/release-keys",
		"ro.vendor.build.fingerprint=Xiaomi/plato_global/plato:12/SP1A.210812.016/V816.0.12.0.ULQMIXM:user/release-keys",
		"ro.product.system.device=generic", "ro.product.vendor.name=plato_global", "ro.build.type=user",
	}
	// realBP 里 system.device 本身就是占位符 generic（小米 mainline 镜像的毛病），要被指纹里的 missi 盖掉；
	// vendor.brand 是真值，不动。
	got := " " + strings.Join(partitionIdentity(args, map[string]string{"ro.product.vendor.brand": "Xiaomi", "ro.product.system.device": "generic"}), " ") + " "
	for _, want := range []string{"ro.product.system.device=missi", "ro.product.system.name=missi_phone_global", "ro.product.system.brand=Android", "ro.product.vendor.device=plato", "ro.build.type=user"} {
		if !strings.Contains(got, " "+want+" ") {
			t.Fatalf("没有 %s：%s", want, got)
		}
	}
	if strings.Contains(got, "device=generic") || strings.Contains(got, "ro.product.vendor.brand=") {
		t.Fatalf("generic 要被盖掉、真机数据里有的不动：%s", got)
	}
}
