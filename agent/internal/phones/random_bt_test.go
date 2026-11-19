package phones

import (
	"fmt"
	"strings"
	"testing"
)

func TestRandBTMACVendorOUI(t *testing.T) {
	m := &Model{Values: map[string]any{"brand": "POCO", "manufacturer": "Xiaomi"}}
	for i := 0; i < 50; i++ {
		mac := randBTMAC(m)
		if !validMAC(mac) || mac != strings.ToUpper(mac) {
			t.Fatalf("格式不对（要大写、合法）：%s", mac)
		}
		ok := false
		for _, o := range btOUIs["xiaomi"] {
			if strings.HasPrefix(mac, o) {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("小米系机型蓝牙地址应是小米 OUI：%s", mac)
		}
	}
	// 没有已知 OUI：全球管理、单播（首字节最低两位为 0）
	mac := randBTMAC(&Model{Values: map[string]any{"brand": "Nothing"}})
	var b0 int
	if _, err := fmt.Sscanf(mac[:2], "%X", &b0); err != nil {
		t.Fatal(err)
	}
	if b0&0b11 != 0 {
		t.Fatalf("无 OUI 时应是全球管理单播地址：%s", mac)
	}
	if mac != strings.ToUpper(mac) || !validMAC(mac) {
		t.Fatalf("格式不对：%s", mac)
	}
}
