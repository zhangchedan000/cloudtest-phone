package phones

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// 「随机」模式的生成规则，和管理页 web/src/data/random.ts 保持一致（取值都在真实手机的范围内）。
// TODO：以后管理页的「重新随机」直接调管家程序接口，规则只保留这一份。

func randInt(min, max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	return min + int(n.Int64())
}

func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

func pick[T any](list []T) T { return list[randInt(0, len(list)-1)] }

// 安卓 10+ 的 WiFi MAC 按网络随机化：第一个字节是「本地管理」地址。
func randMAC() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	b[0] = (b[0] &^ 0b11) | 0b10
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

// 蓝牙地址是出厂烧录的「全球唯一」地址：已知厂商用它真实的 OUI 前缀、大写；其余用全球管理的随机地址。
// 以前和 WiFi 共用 randMAC（本地管理位 + 小写），真机蓝牙地址不会是这样（2026-11-18 体检）。
var btOUIs = map[string][]string{
	"xiaomi": {"28:6C:07", "64:09:80", "50:8F:4C", "F8:A4:5F", "9C:99:A0", "AC:F7:F3"},
}

func randBTMAC(m *Model) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	if m != nil {
		for _, k := range []string{"manufacturer", "brand"} {
			key := strings.ToLower(str(m.Values[k]))
			if key == "poco" || key == "redmi" {
				key = "xiaomi"
			}
			if ouis := btOUIs[key]; len(ouis) > 0 {
				return fmt.Sprintf("%s:%02X:%02X:%02X", pick(ouis), b[3], b[4], b[5])
			}
		}
	}
	b[0] &^= 0b11 // 没有已知 OUI：至少是全球管理、单播地址（不是一眼假的本地管理位）
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", b[0], b[1], b[2], b[3], b[4], b[5])
}

func randSerial() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ0123456789"
	n := pick([]int{8, 10, 12})
	out := make([]byte, n)
	for i := range out {
		out[i] = chars[randInt(0, len(chars)-1)]
	}
	return string(out)
}

func randUUID() string {
	h := randHex(32)
	return fmt.Sprintf("%s-%s-4%s-%s%s-%s", h[0:8], h[8:12], h[13:16], pick([]string{"8", "9", "a", "b"}), h[17:20], h[20:32])
}

func randKernel(m *Model) string {
	shipped := 12
	if m != nil && len(m.AndroidVersions) > 0 {
		shipped = m.AndroidVersions[0]
	}
	var base string
	switch {
	case shipped >= 14:
		base = fmt.Sprintf("6.1.%d-android14-11", randInt(57, 118))
	case shipped >= 13:
		base = fmt.Sprintf("5.15.%d-android13-8", randInt(94, 153))
	default:
		base = fmt.Sprintf("5.10.%d-android12-9", randInt(160, 218))
	}
	return base + "-g" + randHex(12)
}

// RandomValue 返回 nil 表示这个字段没有随机规则。
func RandomValue(key string, m *Model) any {
	switch key {
	case "androidId":
		return randHex(16)
	case "serial":
		return randSerial()
	case "wifiMac":
		return randMAC()
	case "bluetoothMac":
		return randBTMAC(m)
	case "gaid":
		return randUUID()
	case "deviceName":
		if m != nil {
			if v, ok := m.Values["marketName"].(string); ok {
				return v
			}
		}
		return "Android"
	case "batteryLevel":
		return randInt(23, 96)
	case "batteryCharging":
		return pick([]string{"none", "none", "none", "ac", "usb"})
	case "batteryTemp":
		return float64(randInt(270, 355)) / 10
	case "wifiRssi":
		return randInt(-72, -48)
	case "wifiSsid":
		return pick([]string{"NETGEAR", "Spectrum", "ATT", "xfinitywifi", "TP-Link", "MySpectrumWiFi"}) + pick([]string{"", "-5G", "_" + randHex(2), fmt.Sprint(randInt(10, 99))})
	case "kernelRelease":
		return randKernel(m)
	case "kernelBuild":
		return "#1 SMP PREEMPT " + time.Now().AddDate(0, 0, -randInt(60, 400)).UTC().Format("Mon Jan 2 15:04:05 UTC 2006")
	}
	return nil
}
