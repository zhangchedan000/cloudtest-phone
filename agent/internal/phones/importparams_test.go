package phones

import (
	"context"
	"strings"
	"testing"
)

const realDump = `
[ro.product.brand]: [Redmi]
[ro.product.manufacturer]: [Xiaomi]
[ro.product.model]: [23049RAD8C]
[ro.product.device]: [sapphire]
[ro.product.marketname]: [Redmi K50]
[ro.build.fingerprint]: [Redmi/sapphire/sapphire:14/UKQ1.230804.001/OS1.0.5.0.UMKMIXM:user/release-keys]
[ro.build.version.security_patch]: [2024-07-01]
[ro.soc.model]: [MT6895]
[ro.soc.manufacturer]: [Mediatek]
[ro.serialno]: [abcd1234efgh]
[ro.device.wifi_mac]: [32:74:06:a9:50:30]
[ro.boot.btmacaddr]: [40:DE:24:67:5C:9D]
[ro.hardware]: [mt6895]
[some.noise]: [ignore]
`

func TestImportDeviceParams(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, err := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err != nil {
		t.Fatal(err)
	}
	imported, _, err := svc.ImportDeviceParams(p.ID, realDump)
	if err != nil {
		t.Fatal(err)
	}
	// 关键身份字段应被导入
	impSet := strings.Join(imported, ",")
	for _, want := range []string{"brand", "model", "device", "fingerprint", "serial", "wifiMac", "bluetoothMac", "socModel"} {
		if !strings.Contains(","+impSet+",", ","+want+",") {
			t.Fatalf("字段 %q 没导入，imported=%v", want, imported)
		}
	}
	// 验证存进了手机、模式为 manual
	ph := mustPhone(t, svc, p.ID)
	if fs := ph.Fields["brand"]; fs.Mode != "manual" || fs.Value != "Redmi" {
		t.Fatalf("brand 没套成 Redmi(manual)：%+v", fs)
	}
	if fs := ph.Fields["device"]; fs.Value != "sapphire" {
		t.Fatalf("device 应为 sapphire：%+v", fs)
	}
	// 导入的指纹要真的进启动参数（BootArgs 优先用手动指纹）
	joined := strings.Join(svc.BootArgs(*ph, svc.store.Snapshot()), " ")
	wantFP := "Redmi/sapphire/sapphire:14/UKQ1.230804.001/OS1.0.5.0.UMKMIXM:user/release-keys"
	if !strings.Contains(joined, "ro.build.fingerprint="+wantFP) || !strings.Contains(joined, "ro.vendor.build.fingerprint="+wantFP) {
		t.Fatalf("导入的指纹没进启动参数：\n%s", joined)
	}
	// 能力字段（CPU/屏幕）不应被 getprop 导入覆盖（dump 里没有也不碰）
	if fs, ok := ph.Fields["cpuParts"]; ok && fs.Mode == "manual" {
		t.Fatal("cpuParts 不该被 getprop 导入")
	}

	// 启动能生成容器（套了真机参数还能正常开机）
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatalf("套真机参数后开机失败：%v", err)
	}
}

func TestParseGetpropDumpFormats(t *testing.T) {
	// getprop 格式 + key=value 混合
	props := parseGetpropDump("[ro.product.brand]: [POCO]\nro.product.model=22041216G\nro.build.id: UP1A\n杂线忽略")
	if props["ro.product.brand"] != "POCO" || props["ro.product.model"] != "22041216G" {
		t.Fatalf("解析不对：%+v", props)
	}
}
