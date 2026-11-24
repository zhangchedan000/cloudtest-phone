package wifi

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPlanForStableAndRealistic(t *testing.T) {
	a, err := PlanFor("378ad10f54b8", "64:a2:00:3c:5e:71", "/data")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := PlanFor("378ad10f54b8", "64:a2:00:3c:5e:71", "/data")
	if !reflect.DeepEqual(a, b) {
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
	DelRadioFunc = func(string, string) error { return nil }
	useSystemd = func() bool { return false }
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
	// sta + ap 两块主网卡 + 若干假邻居网卡，全都要在路由器命名空间里建（隔离）。
	if len(radios) < 2 {
		t.Fatalf("网卡太少：%v", radios)
	}
	for _, rr := range radios {
		if !strings.HasPrefix(rr, "/run/netns/ppr-378ad10f54b8 ") {
			t.Fatalf("网卡都要在路由器命名空间里建（隔离）：%v", radios)
		}
	}
	j := strings.Join(r.calls, "\n")
	idx := func(s string) int { return strings.Index(j, s) }
	if idx("interface add wlan0") > idx("set netns 4242") {
		t.Error("wlan0 要在路由器里建好再连网卡一起搬进手机（iw 在外面找不到已经搬走的网卡）")
	}
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

// 有 systemd 时热点 / DHCP 作为临时服务跑（不随管家重启被杀），关机时停掉服务。
type sdRun struct{ fakeRun }

func (f *sdRun) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := f.fakeRun.Run(ctx, name, args...)
	if name == "systemctl" && len(args) > 0 && args[0] == "is-active" {
		return "active\n", nil
	}
	return out, err
}

func TestSetupWithSystemd(t *testing.T) {
	DelRadioFunc = func(string, string) error { return nil }
	useSystemd = func() bool { return true }
	defer func() { useSystemd = func() bool { return false } }()
	p, _ := PlanFor("378ad10f54b8", "64:a2:00:3c:5e:71", t.TempDir())
	r := &sdRun{}
	if err := Setup(context.Background(), r, func(string, string, [6]byte) (int, error) { return 1, nil }, p, 4242, "10.88.0.1", "10.88.0.1:1053"); err != nil {
		t.Fatal(err)
	}
	j := strings.Join(r.calls, "\n")
	for _, want := range []string{"systemd-run --unit=ppwifi-378ad10f54b8-hostapd", "-p Restart=always", "ip netns exec ppr-378ad10f54b8 hostapd -f", "systemd-run --unit=ppwifi-378ad10f54b8-dnsmasq", "--keep-in-foreground"} {
		if !strings.Contains(j, want) {
			t.Errorf("缺 %q：\n%s", want, j)
		}
	}
	if strings.Contains(j, "hostapd -B") {
		t.Error("systemd 管的 hostapd 不能 -B（会被当成已退出）")
	}
	r.calls = nil
	_ = Teardown(context.Background(), r, p)
	if !strings.Contains(strings.Join(r.calls, "\n"), "systemctl stop ppwifi-378ad10f54b8-hostapd ppwifi-378ad10f54b8-dnsmasq") {
		t.Errorf("关机要停掉服务：%v", r.calls)
	}
	// 服务在跑就不动
	r.calls = nil
	if _, err := os.Stat("/run/netns/" + p.Netns); err == nil {
		t.Skip("这台机器上真有这个命名空间")
	}
	if fixed, _ := EnsureDaemons(context.Background(), r, p); fixed {
		t.Error("路由器不在时不该补")
	}
}

// 快速关机再开机：旧网卡还没删掉时新网卡要用不同的名字，Teardown 要按前缀清掉漏下的（踩坑 #57-11）。
func TestRadioNamesUniqueAndLeftoversCleaned(t *testing.T) {
	useSystemd = func() bool { return false }
	sys := t.TempDir()
	SysIeee80211 = sys
	defer func() { SysIeee80211 = "/sys/class/ieee80211" }()
	p, _ := PlanFor("378ad10f54b8", "64:a2:00:3c:5e:71", t.TempDir())
	// 宿主机上有这台手机上次漏下的网卡，还有别的手机的
	for _, n := range []string{p.StaPhy + "ab12", p.APPhy + "ab12", "pps999999999990000"} {
		_ = os.MkdirAll(filepath.Join(sys, n), 0o755)
	}
	var deleted []string
	DelRadioFunc = func(ns, name string) error { deleted = append(deleted, name); return nil }
	var created []string
	newRadio := func(ns, name string, mac [6]byte) (int, error) { created = append(created, name); return 1, nil }
	if err := Setup(context.Background(), &fakeRun{}, newRadio, p, 4242, "10.88.0.1", "10.88.0.1:1053"); err != nil {
		t.Fatal(err)
	}
	d := strings.Join(deleted, " ")
	if !strings.Contains(d, p.StaPhy+"ab12") || !strings.Contains(d, p.APPhy+"ab12") || strings.Contains(d, "pps99999") {
		t.Fatalf("应只清掉这台手机漏下的网卡：%v", deleted)
	}
	if len(created) < 2 || created[0] == p.StaPhy || !strings.HasPrefix(created[0], p.StaPhy) {
		t.Fatalf("新网卡名要带随机后缀：%v", created)
	}
	b, _ := os.ReadFile(filepath.Join(p.Dir, "radios"))
	// radios 文件要含 sta + ap 两块主网卡（后面还可能跟假邻居网卡）
	if !strings.Contains(string(b), created[0]) || !strings.Contains(string(b), created[1]) {
		t.Fatalf("radios 文件：%q", b)
	}
	deleted = nil
	_ = Teardown(context.Background(), &fakeRun{}, p)
	if !strings.Contains(strings.Join(deleted, " "), created[0]) {
		t.Fatalf("关机要按记录删这次的网卡：%v", deleted)
	}
}
