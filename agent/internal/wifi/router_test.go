package wifi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanForStableAndRealistic(t *testing.T) {
	a, err := PlanFor("378ad10f54b8", "64:a2:00:3c:5e:71", "/data")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := PlanFor("378ad10f54b8", "64:a2:00:3c:5e:71", "/data")
	if a != b {
		t.Fatal("同一台手机的 WiFi 配置每次都应该一样")
	}
	c, _ := PlanFor("6fa04a04f5ba", "64:a2:00:3c:5e:72", "/data")
	if a.SSID == c.SSID && a.Passphrase == c.Passphrase {
		t.Fatal("不同手机的路由器应该不一样")
	}
	if strings.ContainsAny(a.SSID, " \"'") || len(a.Passphrase) < 8 {
		t.Fatalf("SSID / 密码不合适：%q %q", a.SSID, a.Passphrase)
	}
	if a.Netns != "ppr-378ad10f54b8" || a.StaMAC != [6]byte{0x64, 0xa2, 0x00, 0x3c, 0x5e, 0x71} {
		t.Fatalf("plan 不对：%+v", a)
	}
	if !strings.Contains(a.HostapdConf(), "wpa=2") || !strings.Contains(a.HostapdConf(), "ssid="+a.SSID) {
		t.Fatal("热点应该是 WPA2")
	}
	if _, err := PlanFor("x", "bad", "/data"); err == nil {
		t.Fatal("坏 MAC 应该报错")
	}
}

type fakeRun struct{ calls []string }

func (f *fakeRun) Run(_ context.Context, name string, args ...string) (string, error) {
	c := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, c)
	if strings.Contains(c, "addr show eth0") {
		return "2: eth0    inet 10.88.1.4/16 brd 10.88.255.255 scope global eth0", nil
	}
	return "", nil
}

func TestSetupOrderAndCommands(t *testing.T) {
	dir := t.TempDir()
	p, _ := PlanFor("378ad10f54b8", "64:a2:00:3c:5e:71", dir)
	r := &fakeRun{}
	var radios []string
	newRadio := func(ns, name string, mac [6]byte) (int, error) {
		radios = append(radios, ns+" "+name)
		return len(radios), nil
	}
	if err := Setup(context.Background(), r, newRadio, p, 4242, "10.88.0.1", "10.88.0.1:1053"); err != nil {
		t.Fatal(err)
	}
	if len(radios) != 2 || !strings.HasPrefix(radios[0], "/run/netns/ppr-378ad10f54b8 ") {
		t.Fatalf("两块网卡都要在路由器命名空间里建（隔离）：%v", radios)
	}
	j := strings.Join(r.calls, "\n")
	idx := func(s string) int { return strings.Index(j, s) }
	for _, want := range []string{"ip netns add ppr-378ad10f54b8", "set netns 4242", "interface add wlan0 type managed", "hostapd -B", "dnsmasq --interface=wlan1", "nft -f", "ip link set eth0 netns ppr-378ad10f54b8", "ip addr add 10.88.1.4/16 dev eth0", "default via 10.88.0.1 dev eth0"} {
		if idx(want) < 0 {
			t.Errorf("缺 %q：\n%s", want, j)
		}
	}
	if idx("ip link set eth0 netns") < idx("hostapd -B") || idx("ip link set eth0 netns") < idx("nft -f") {
		t.Error("eth0 要最后才搬（前面失败时手机还能用原来的网络）")
	}
	nft, _ := os.ReadFile(filepath.Join(p.Dir, "nft.conf"))
	if !strings.Contains(string(nft), "snat to 10.88.1.4") || !strings.Contains(string(nft), "dnat to 10.88.0.1:1053") || !strings.Contains(string(nft), "dport 5555 dnat to "+p.PhoneIP()) {
		t.Fatalf("NAT 规则不对：\n%s", nft)
	}
}
