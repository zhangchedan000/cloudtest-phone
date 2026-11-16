package wifi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Runner 执行宿主机命令（测试时换成假的）。
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// Plan 一台手机的 WiFi 配置。除了 MAC，全都由手机 ID 算出来（固定不变：手机每次开机连的都是「同一个家里的路由器」，
// 安卓保存的 WiFi 配置一直有效）。
type Plan struct {
	PhoneID    string
	Netns      string  // 路由器网络命名空间名：ppr-<ID>
	StaPhy     string  // 手机那块 hwsim 网卡的 phy 名
	APPhy      string  // 路由器那块的 phy 名
	StaMAC     [6]byte // 手机 WiFi MAC（机型档案 wifiMac）
	APMAC      [6]byte // 路由器 MAC（BSSID），带常见路由器厂商的前缀
	SSID       string
	Passphrase string
	Subnet     int    // 192.168.<Subnet>.0/24
	PhoneHost  int    // 手机拿到的地址 192.168.<Subnet>.<PhoneHost>
	Channel    int    // 1 / 6 / 11
	Dir        string // 配置、pid、日志放这里
}

// 常见家用路由器：SSID 前缀和 MAC 前缀（OUI）一一对应，免得「TP-Link 的名字配华为的 MAC」。
var routers = []struct {
	ssid string
	oui  [3]byte
}{
	{"TP-Link_", [3]byte{0x50, 0xc7, 0xbf}},
	{"TP-LINK_", [3]byte{0x98, 0xda, 0xc4}},
	{"NETGEAR", [3]byte{0x9c, 0x3d, 0xcf}},
	{"ASUS_", [3]byte{0x04, 0xd9, 0xf5}},
	{"HUAWEI-", [3]byte{0x48, 0x8e, 0xef}},
	{"Xiaomi_", [3]byte{0x50, 0x64, 0x2b}},
	{"Linksys", [3]byte{0x58, 0xef, 0x68}},
}

var subnets = []int{0, 1, 1, 0, 31, 3, 50, 100, 8, 2}

// PlanFor 按手机 ID 和 WiFi MAC 生成配置。
func PlanFor(phoneID, wifiMAC, dataDir string) (Plan, error) {
	mac, err := ParseMAC(wifiMAC)
	if err != nil {
		return Plan{}, err
	}
	h := sha256.Sum256([]byte("prism-wifi:" + phoneID))
	r := routers[int(h[0])%len(routers)]
	short := phoneID
	if len(short) > 10 {
		short = short[:10]
	}
	p := Plan{
		PhoneID:   phoneID,
		Netns:     "ppr-" + phoneID,
		StaPhy:    "pps" + short,
		APPhy:     "ppa" + short,
		StaMAC:    mac,
		APMAC:     [6]byte{r.oui[0], r.oui[1], r.oui[2], h[1], h[2], h[3]},
		Subnet:    subnets[int(h[4])%len(subnets)],
		PhoneHost: 100 + int(h[5])%100,
		Channel:   []int{1, 6, 11}[int(h[6])%3],
		Dir:       filepath.Join(dataDir, "phones", phoneID, "wifi"),
	}
	suffix := strings.ToUpper(fmt.Sprintf("%02x%02x", h[7], h[8]))
	switch r.ssid {
	case "NETGEAR", "Linksys":
		p.SSID = r.ssid + strconv.Itoa(10+int(h[7])%90)
	default:
		p.SSID = r.ssid + suffix
	}
	const letters = "abcdefghjkmnpqrstuvwxyz23456789"
	pass := make([]byte, 12)
	for i := range pass {
		pass[i] = letters[int(h[9+i])%len(letters)]
	}
	p.Passphrase = string(pass)
	return p, nil
}

// ParseMAC 解析 aa:bb:cc:dd:ee:ff。
func ParseMAC(s string) ([6]byte, error) {
	var m [6]byte
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 6 {
		return m, fmt.Errorf("WiFi MAC 格式不对：%q", s)
	}
	for i, p := range parts {
		v, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return m, fmt.Errorf("WiFi MAC 格式不对：%q", s)
		}
		m[i] = byte(v)
	}
	return m, nil
}

func macString(m [6]byte) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", m[0], m[1], m[2], m[3], m[4], m[5])
}

// RouterIP / PhoneIP 路由器、手机在 WiFi 里的地址。
func (p Plan) RouterIP() string { return fmt.Sprintf("192.168.%d.1", p.Subnet) }
func (p Plan) PhoneIP() string  { return fmt.Sprintf("192.168.%d.%d", p.Subnet, p.PhoneHost) }

// HostapdConf 热点配置（WPA2 个人，和普通家用路由器一样）。
func (p Plan) HostapdConf() string {
	return fmt.Sprintf(`interface=wlan1
driver=nl80211
ssid=%s
hw_mode=g
channel=%d
ieee80211n=1
wmm_enabled=1
auth_algs=1
wpa=2
wpa_key_mgmt=WPA-PSK
rsn_pairwise=CCMP
wpa_passphrase=%s
`, p.SSID, p.Channel, p.Passphrase)
}

// NewRadioFunc 建 hwsim 网卡（测试时换成假的）。
type NewRadioFunc func(netnsPath, name string, mac [6]byte) (int, error)

// Setup 给一台刚启动的手机配好 WiFi：containerPid 是手机容器的进程号，gateway / dnsTarget 是宿主机网关和 mihomo DNS。
// 顺序：先建路由器、网卡、热点、DHCP（这些失败都不影响手机原来的 eth0），最后才把 eth0 搬进路由器。
func Setup(ctx context.Context, run Runner, newRadio NewRadioFunc, p Plan, containerPid int, gateway, dnsTarget string) error {
	pid := strconv.Itoa(containerPid)
	in := func(args ...string) []string { return append([]string{"netns", "exec", p.Netns}, args...) }
	inPhone := func(args ...string) []string { return append([]string{"-t", pid, "-n"}, args...) }
	step := func(what, name string, args ...string) error {
		if out, err := run.Run(ctx, name, args...); err != nil {
			return fmt.Errorf("%s：%v %s", what, err, strings.TrimSpace(out))
		}
		return nil
	}
	_ = Teardown(ctx, run, p)
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return err
	}
	if err := step("建路由器网络命名空间", "ip", "netns", "add", p.Netns); err != nil {
		return err
	}
	if err := step("路由器 lo", "ip", in("ip", "link", "set", "lo", "up")...); err != nil {
		return err
	}
	nsPath := "/run/netns/" + p.Netns
	if _, err := newRadio(nsPath, p.StaPhy, p.StaMAC); err != nil {
		return fmt.Errorf("建手机的虚拟无线网卡：%w", err)
	}
	if _, err := newRadio(nsPath, p.APPhy, p.APMAC); err != nil {
		return fmt.Errorf("建路由器的虚拟无线网卡：%w", err)
	}
	// iw 按名字找网卡要读 /sys，网卡搬进手机后从外面的 /sys 看不到（踩坑 #57）：
	// 所以在路由器命名空间里先把 wlan0 建好，再把网卡连同 wlan0 一起搬进手机。
	// 刚建的网卡在 /sys 里出现要一点时间（机器忙时实测会 ENOENT），稍等重试
	var err error
	for i := 0; i < 10; i++ {
		if err = step("手机 wlan0", "ip", in("iw", "phy", p.StaPhy, "interface", "add", "wlan0", "type", "managed")...); err == nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	if err := step("网卡搬进手机", "ip", in("iw", "phy", p.StaPhy, "set", "netns", pid)...); err != nil {
		return err
	}
	if err := step("路由器 wlan1", "ip", in("iw", "phy", p.APPhy, "interface", "add", "wlan1", "type", "managed")...); err != nil {
		return err
	}
	if err := step("路由器地址", "ip", in("ip", "addr", "add", p.RouterIP()+"/24", "dev", "wlan1")...); err != nil {
		return err
	}
	if err := step("路由器 wlan1 启用", "ip", in("ip", "link", "set", "wlan1", "up")...); err != nil {
		return err
	}
	conf := filepath.Join(p.Dir, "hostapd.conf")
	if err := os.WriteFile(conf, []byte(p.HostapdConf()), 0o600); err != nil {
		return err
	}
	if err := step("热点（hostapd）", "ip", in("hostapd", "-B", "-P", filepath.Join(p.Dir, "hostapd.pid"), "-f", filepath.Join(p.Dir, "hostapd.log"), conf)...); err != nil {
		return err
	}
	if err := step("DHCP（dnsmasq）", "ip", in("dnsmasq", "--interface=wlan1", "--bind-interfaces", "--except-interface=lo",
		fmt.Sprintf("--dhcp-range=192.168.%d.100,192.168.%d.199,12h", p.Subnet, p.Subnet),
		fmt.Sprintf("--dhcp-host=%s,%s", macString(p.StaMAC), p.PhoneIP()),
		"--dhcp-option=6,"+p.RouterIP(), "--port=0",
		"--pid-file="+filepath.Join(p.Dir, "dnsmasq.pid"), "--dhcp-leasefile="+filepath.Join(p.Dir, "leases"))...); err != nil {
		return err
	}
	if err := step("路由器转发", "ip", in("sysctl", "-qw", "net.ipv4.ip_forward=1")...); err != nil {
		return err
	}
	// 上行：手机原来的 eth0（10.88.x）搬进路由器。搬过去地址会丢，先记下来再加回去。
	addr, err := run.Run(ctx, "nsenter", inPhone("ip", "-4", "-o", "addr", "show", "eth0")...)
	if err != nil {
		return fmt.Errorf("读手机 eth0 地址：%v %s", err, addr)
	}
	fields := strings.Fields(addr)
	cidr := ""
	for i, f := range fields {
		if f == "inet" && i+1 < len(fields) {
			cidr = fields[i+1]
		}
	}
	if cidr == "" {
		return fmt.Errorf("手机 eth0 没有 IPv4 地址：%s", addr)
	}
	uplinkIP := strings.SplitN(cidr, "/", 2)[0]
	rules := fmt.Sprintf(`table ip ppwifi {
  chain pre {
    type nat hook prerouting priority dstnat; policy accept;
    iifname "wlan1" ip daddr %[1]s udp dport 53 dnat to %[2]s
    iifname "wlan1" ip daddr %[1]s tcp dport 53 dnat to %[2]s
    iifname "eth0" tcp dport 5555 dnat to %[3]s:5555
  }
  chain post {
    type nat hook postrouting priority srcnat; policy accept;
    ip saddr 192.168.%[4]d.0/24 oifname "eth0" snat to %[5]s
  }
}
`, p.RouterIP(), dnsTarget, p.PhoneIP(), p.Subnet, uplinkIP)
	rf := filepath.Join(p.Dir, "nft.conf")
	if err := os.WriteFile(rf, []byte(rules), 0o600); err != nil {
		return err
	}
	if err := step("路由器 NAT", "ip", in("nft", "-f", rf)...); err != nil {
		return err
	}
	if err := step("手机 eth0 停用", "nsenter", inPhone("ip", "link", "set", "eth0", "down")...); err != nil {
		return err
	}
	if err := step("eth0 搬进路由器", "nsenter", inPhone("ip", "link", "set", "eth0", "netns", p.Netns)...); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"ip", "addr", "add", cidr, "dev", "eth0"},
		{"ip", "link", "set", "eth0", "up"},
		{"ip", "route", "replace", "default", "via", gateway, "dev", "eth0"},
	} {
		if err := step("路由器上行", "ip", in(args...)...); err != nil {
			return err
		}
	}
	return nil
}

// DelRadioFunc 删 hwsim 网卡（测试时换成假的）。
var DelRadioFunc = DelRadioInNetns

// Teardown 停掉热点 / DHCP，删掉这台的两块 hwsim 网卡和路由器网络命名空间。没有就什么都不做。
// 实测删命名空间不会自动删网卡（会一直留着），所以要显式删：路由器那块在路由器命名空间里删；
// 手机那块在手机关机后回到管家所在的命名空间，在这里删。
func Teardown(ctx context.Context, run Runner, p Plan) error {
	if p.APPhy != "" {
		_ = DelRadioFunc("/run/netns/"+p.Netns, p.APPhy)
		_ = DelRadioFunc("", p.APPhy)
	}
	if p.StaPhy != "" {
		_ = DelRadioFunc("", p.StaPhy)
	}
	for _, f := range []string{"hostapd.pid", "dnsmasq.pid"} {
		if b, err := os.ReadFile(filepath.Join(p.Dir, f)); err == nil {
			if pid := strings.TrimSpace(string(b)); pid != "" {
				_, _ = run.Run(ctx, "kill", pid)
			}
			_ = os.Remove(filepath.Join(p.Dir, f))
		}
	}
	if _, err := os.Stat("/run/netns/" + p.Netns); err == nil {
		if out, err := run.Run(ctx, "ip", "netns", "del", p.Netns); err != nil {
			return fmt.Errorf("删路由器：%v %s", err, out)
		}
	}
	return nil
}

// HostReady 宿主机能做虚拟 WiFi 吗：需要 root、hwsim 模块、iw / hostapd / dnsmasq / nft / nsenter。
// 顺带把 hwsim 加载成「不自动建网卡」（radios=0），网卡都由 Setup 按需建。
func HostReady(ctx context.Context, run Runner) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("要 root")
	}
	for _, c := range []string{"iw", "hostapd", "dnsmasq", "nft", "nsenter", "ip"} {
		if _, err := run.Run(ctx, "sh", "-c", "command -v "+c); err != nil {
			return fmt.Errorf("缺 %s（apt-get install -y iw hostapd dnsmasq-base nftables util-linux iproute2）", c)
		}
	}
	if _, err := os.Stat("/sys/module/mac80211_hwsim"); err != nil {
		if out, err := run.Run(ctx, "modprobe", "mac80211_hwsim", "radios=0"); err != nil {
			return fmt.Errorf("内核没有 mac80211_hwsim：%v %s", err, out)
		}
	}
	// WPA2 热点要 AES-CCMP（ccm 模块）。有的内核不会自动加载，缺了 hostapd 报 "key addition failed"（踩坑 #57）
	if _, err := os.Stat("/sys/module/ccm"); err != nil {
		if out, err := run.Run(ctx, "modprobe", "ccm"); err != nil {
			return fmt.Errorf("加载 ccm（WPA2 加密）失败：%v %s", err, out)
		}
	}
	return nil
}
