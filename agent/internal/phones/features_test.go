package phones

import (
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/sensors"
)

func TestPlanFeatures(t *testing.T) {
	img := map[string]int{"android.hardware.ethernet": 0, "android.hardware.wifi": 0, "android.hardware.telephony.calling": 0,
		"android.hardware.vulkan.level": 1, "android.hardware.hardware_keystore": 41, "android.software.controls": 0,
		"android.hardware.sensor.accelerometer": 0, "com.google.android.feature.GOOGLE_BUILD": 0}
	real := parseFeatureList([]string{"android.hardware.wifi", "android.hardware.bluetooth_le", "android.hardware.nfc",
		"android.hardware.sensor.gyroscope", "android.hardware.sensor.stepcounter", "android.hardware.vulkan.level=0",
		"android.hardware.hardware_keystore=100", "android.hardware.consumerir", "android.software.verified_boot",
		"android.software.virtualization_framework", "android.software.incremental_delivery=2", "com.vivo.themestore.SUPPORTED",
		"cn.google.services", "android.sofware.nfc.beam", "android.software.opengles.deqp.level=132449025"})
	add, remove := planFeatures(img, real, sensorFeatures(sensors.DefaultMask))
	for _, want := range []string{"android.hardware.bluetooth_le", "android.hardware.nfc", "android.hardware.sensor.gyroscope",
		"android.software.verified_boot", "com.vivo.themestore.SUPPORTED", "android.software.opengles.deqp.level"} {
		if _, ok := add[want]; !ok {
			t.Errorf("应加 %s：%v", want, add)
		}
	}
	for _, no := range []string{"android.hardware.sensor.stepcounter", "android.hardware.hardware_keystore", "android.hardware.consumerir",
		"android.software.virtualization_framework", "android.software.incremental_delivery", "cn.google.services", "android.sofware.nfc.beam"} {
		if _, ok := add[no]; ok {
			t.Errorf("不该加 %s", no)
		}
	}
	if add["android.software.opengles.deqp.level"] != 132449025 {
		t.Errorf("版本要照真机：%v", add)
	}
	if strings.Join(remove, ",") != "android.hardware.ethernet" {
		t.Errorf("只该去掉 ethernet（电话子特性、显卡、密钥库、在用的传感器不动）：%v", remove)
	}
}

func TestFeaturesXML(t *testing.T) {
	orig := []byte("<?xml version=\"1.0\"?>\n<permissions>\n    <feature name=\"android.hardware.wifi\" />\n</permissions>\n")
	out, err := featuresXML(orig, map[string]int{"android.hardware.nfc": 0, "android.software.midi": 0, "x.y": 3}, []string{"android.hardware.ethernet"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	parseFeatureXML(out, got)
	if len(got) != 4 || got["x.y"] != 3 {
		t.Fatalf("解析结果不对：%v\n%s", got, out)
	}
	if !strings.Contains(string(out), `<unavailable-feature name="android.hardware.ethernet" />`) || !strings.HasSuffix(string(out), "</permissions>\n") {
		t.Fatalf("格式不对：\n%s", out)
	}
}
