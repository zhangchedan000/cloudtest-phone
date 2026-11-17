package wifi

import (
	"strings"
	"testing"
)

func TestPlanForCountry(t *testing.T) {
	mac := "02:11:22:33:44:55"
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		id := "phone" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		jp, err := PlanForCountry(id, mac, "/tmp", "JP")
		if err != nil {
			t.Fatal(err)
		}
		// 名字和 MAC 厂商要配对
		if strings.HasPrefix(jp.SSID, "Buffalo-") && jp.APMAC[0] != 0x10 {
			t.Fatalf("Buffalo 的名字配了别家的 MAC：%s %x", jp.SSID, jp.APMAC)
		}
		de, _ := PlanForCountry(id, mac, "/tmp", "de")
		if strings.HasPrefix(de.SSID, "FRITZ!Box ") && (de.APMAC[0] != 0x3c || len(de.SSID) > 32) {
			t.Fatalf("FRITZ!Box 不对：%q %x", de.SSID, de.APMAC)
		}
		kr, _ := PlanForCountry(id, mac, "/tmp", "KR")
		seen[strings.SplitN(jp.SSID, "-", 2)[0]] = true
		seen[kr.SSID] = true
		// 没检测出国家 = 原来那套（现有手机的 WiFi 名字不变）
		def, _ := PlanFor(id, mac, "/tmp")
		hk, _ := PlanForCountry(id, mac, "/tmp", "HK")
		if def.SSID != hk.SSID {
			t.Fatalf("香港应该和默认一样：%s %s", def.SSID, hk.SSID)
		}
	}
	if !seen["Buffalo"] || !(seen["iptime"] || seen["iptime5G"] || seen["iptime_5G"]) {
		t.Fatalf("日本、韩国应该出现当地牌子：%v", seen)
	}
}
