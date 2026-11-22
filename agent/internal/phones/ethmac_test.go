package phones

import "testing"

func TestEthMAC(t *testing.T) {
	for in, want := range map[string]string{
		"9E:3A:11:22:33:44": "9e:3a:11:22:33:44", // 档案里的 wifiMac，小写
		"02:42:0a:58:01:02": "",                  // Docker 自己的前缀不要
		"01:00:5e:00:00:01": "",                  // 组播地址 Docker 不收
		"zz:zz":             "",
		"":                  "",
	} {
		if got := ethMAC(map[string]any{"wifiMac": in}); got != want {
			t.Errorf("ethMAC(%q) = %q，要 %q", in, got, want)
		}
	}
}
