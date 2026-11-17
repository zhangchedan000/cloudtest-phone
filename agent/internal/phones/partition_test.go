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
	// realBP 里 system.device=generic、system.name=mainline 都是小米 mainline 镜像的占位符，和指纹里的
	// missi / missi_phone_global 对不上，要被指纹盖掉；vendor.brand=Xiaomi 跟指纹一致，不重复加。
	got := " " + strings.Join(partitionIdentity(args, map[string]string{
		"ro.product.vendor.brand":  "Xiaomi",
		"ro.product.system.device": "generic",
		"ro.product.system.name":   "mainline",
	}), " ") + " "
	for _, want := range []string{"ro.product.system.device=missi", "ro.product.system.name=missi_phone_global", "ro.product.system.brand=Android", "ro.product.vendor.device=plato", "ro.build.type=user"} {
		if !strings.Contains(got, " "+want+" ") {
			t.Fatalf("没有 %s：%s", want, got)
		}
	}
	// generic / mainline 要被盖掉；跟指纹一致的真值（vendor.brand=Xiaomi）不重复加。
	if strings.Contains(got, "device=generic") || strings.Contains(got, "system.name=mainline") || strings.Contains(got, "ro.product.vendor.brand=") {
		t.Fatalf("占位符要被盖掉、跟指纹一致的真值不重复加：%s", got)
	}
}
