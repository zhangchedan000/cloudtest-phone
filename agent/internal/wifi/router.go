package wifi

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	Neighbors  []Neighbor // 周围的假邻居热点（多 BSS，只为扫描时不止一个；手机不连它们）
}

// 常见家用路由器：SSID 前缀和 MAC 前缀（OUI）一一对应，免得「TP-Link 的名字配华为的 MAC」。
// style：名字后缀怎么生成（hex4：4 位十六进制；num2：两位数字；fritz：FRITZ!Box 型号 + 两个字母；plain5g：原名或加 5G）。
type router struct {
	ssid, style string
	oui         [3]byte
}

var (
	tplink  = router{"TP-Link_", "hex4", [3]byte{0x50, 0xc7, 0xbf}}
	tplink2 = router{"TP-LINK_", "hex4", [3]byte{0x98, 0xda, 0xc4}}
	netgear = router{"NETGEAR", "num2", [3]byte{0x9c, 0x3d, 0xcf}}
	asus    = router{"ASUS_", "hex4", [3]byte{0x04, 0xd9, 0xf5}}
	huawei  = router{"HUAWEI-", "hex4", [3]byte{0x48, 0x8e, 0xef}}
	xiaomi  = router{"Xiaomi_", "hex4", [3]byte{0x50, 0x64, 0x2b}}
	linksys = router{"Linksys", "num2", [3]byte{0x58, 0xef, 0x68}}
)

// routers 默认（香港、台湾、东南亚等，以及没检测出国家时）。
var routers = []router{tplink, tplink2, netgear, asus, huawei, xiaomi, linksys}

// routersByCountry 当地家里常见的路由器（2026-11-17 用户：主要做热门国家）。没列的国家用 routers。
var routersByCountry = map[string][]router{
	"US": {netgear, netgear, tplink, linksys, asus},
	"CA": {netgear, tplink, linksys, asus},
	"MX": {{"INFINITUM", "hex4", [3]byte{0x48, 0x8e, 0xef}}, {"INFINITUM", "hex4", [3]byte{0x48, 0x8e, 0xef}}, tplink, huawei}, // Telmex 光猫多是华为
	"JP": {{"Buffalo-G-", "hex4", [3]byte{0x10, 0x6f, 0x3f}}, {"Buffalo-A-", "hex4", [3]byte{0x10, 0x6f, 0x3f}}, tplink, asus},
	"KR": {{"iptime", "plain5g", [3]byte{0x00, 0x26, 0x66}}, {"iptime", "plain5g", [3]byte{0x00, 0x26, 0x66}}, tplink, asus},
	"DE": {{"FRITZ!Box ", "fritz", [3]byte{0x3c, 0xa6, 0x2f}}, {"FRITZ!Box ", "fritz", [3]byte{0x3c, 0xa6, 0x2f}}, tplink, netgear},
	"GB": {tplink, netgear, asus, huawei},
	"AU": {netgear, tplink, asus, huawei},
}

// routersFor 按代理出口国家选路由器列表。
func routersFor(country string) []router {
	if l, ok := routersByCountry[strings.ToUpper(country)]; ok {
		return l
	}
	return routers
}

var subnets = []int{0, 1, 1, 0, 31, 3, 50, 100, 8, 2}

// PlanFor 按手机 ID 和 WiFi MAC 生成配置（路由器用默认列表）。
func PlanFor(phoneID, wifiMAC, dataDir string) (Plan, error) {
	return PlanForCountry(phoneID, wifiMAC, dataDir, "")
}

// PlanForCountry 同上，路由器按代理出口国家选当地常见的牌子（换了国家的代理，下次开机连的是那边的「家里路由器」）。
func PlanForCountry(phoneID, wifiMAC, dataDir, country string) (Plan, error) {
	mac, err := ParseMAC(wifiMAC)
	if err != nil {
		return Plan{}, err
	}
	h := sha256.Sum256([]byte("prism-wifi:" + phoneID))
	list := routersFor(country)
	r := list[int(h[0])%len(list)]
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
	p.SSID = ssidForRouter(r, h)
	p.Passphrase = wifiPass(h, 9)
	p.Neighbors = neighborsFor(phoneID, list, p.SSID)
	return p, nil
}

// ssidForRouter 按路由器牌子的命名风格 + 哈希生成一个 SSID。
func ssidForRouter(r router, h [32]byte) string {
	switch r.style {
	case "num2":
		return r.ssid + strconv.Itoa(10+int(h[7])%90)
	case "fritz":
		models := []string{"7590", "7530", "7590 AX", "6660 Cable", "7490"}
		return r.ssid + models[int(h[7])%len(models)] + " " + string(rune('A'+int(h[8])%26)) + string(rune('A'+int(h[9])%26))
	case "plain5g":
		return r.ssid + []string{"", "", "5G", "_5G"}[int(h[7])%4]
	default:
		return r.ssid + strings.ToUpper(fmt.Sprintf("%02x%02x", h[7], h[8]))
	}
}

// wifiPass 从哈希取一段 12 位 WPA2 口令（给自己的热点，也给假邻居——邻居的口令没人用，只是让它像个加密网络）。
func wifiPass(h [32]byte, off int) string {
	const letters = "abcdefghjkmnpqrstuvwxyz23456789"
	pass := make([]byte, 12)
	for i := range pass {
		pass[i] = letters[int(h[(off+i)%len(h)])%len(letters)]
	}
	return string(pass)
}

// Neighbor 周围的一个假邻居热点（只为了扫描时不止一个；手机不连它，口令随机）。
type Neighbor struct {
	SSID       string
	Passphrase string
}

// neighborsFor 按手机 ID 稳定生成 2~4 个假邻居热点（真机周围本来就有好几个 WiFi；只有一个反而是破绽 pp-probe#154）。
// 名字用同一套常见路由器牌子，和自己的热点及彼此都不重名。固定不变：同一台手机每次扫到的邻居一样（真实环境也稳定）。
func neighborsFor(phoneID string, list []router, selfSSID string) []Neighbor {
	nh := sha256.Sum256([]byte("prism-wifi-nbr:" + phoneID))
	count := 2 + int(nh[0])%3 // 2~4 个
	seen := map[string]bool{selfSSID: true}
	var out []Neighbor
	for i := 0; len(out) < count && i < count*4; i++ {
		hh := sha256.Sum256([]byte(fmt.Sprintf("prism-wifi-nbr:%s:%d", phoneID, i)))
		r := list[int(hh[0])%len(list)]
		s := ssidForRouter(r, hh)
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, Neighbor{SSID: s, Passphrase: wifiPass(hh, 11)})
	}
	return out
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

// HostapdConf 热点配置（WPA2 个人，和普通家用路由器一样）。自己这个热点手机会连；
// 后面再挂几个假邻居热点（多 BSS，同一块电台广播多个 SSID），让手机扫描时看到好几个网络，
// 不是光秃秃一个（真机周围一般有好几个 WiFi，只有一个是 pp-probe#154 的破绽）。
func (p Plan) HostapdConf() string {
	var b strings.Builder
	fmt.Fprintf(&b, `interface=wlan1
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
	// 假邻居热点：每个一个额外 BSS（BSSID 由电台基址自动递增）。手机不连它们。
	for i, n := range p.Neighbors {
		fmt.Fprintf(&b, `
bss=wlan1_%d
ssid=%s
ieee80211n=1
wmm_enabled=1
auth_algs=1
wpa=2
wpa_key_mgmt=WPA-PSK
rsn_pairwise=CCMP
wpa_passphrase=%s
`, i+1, n.SSID, n.Passphrase)
	}
	return b.String()
}

// NewRadioFunc 建 hwsim 网卡（测试时换成假的）。
type NewRadioFunc func(netnsPath, name string, mac [6]byte) (int, error)

// Setup 给一台刚启动的手机配好 WiFi：containerPid 是手机容器的进程号，gateway / dnsTarget 是宿主机网关和 mihomo DNS。
// 顺序：先建路由器、网卡、热点、DHCP（这些失败都不影响手机原来的 eth0），最后才把 eth0 搬进路由器。
func Setup(ctx context.Context, run Runner, newRadio NewRadioFunc, p Plan, containerPid int, gateway, dnsTarget string) (err error) {
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
	// 每次开机网卡名加随机后缀（踩坑 #57-11）：手机刚关机时，内核把它那块网卡还回宿主机是异步的，
	// 马上再开机时旧网卡还占着同名，新网卡会被内核悄悄改名成 phyN，后面按名字就找不到了。
	// 名字记在 radios 文件里，Teardown 按记录和前缀把这台的网卡都清掉。
	var sfx [2]byte
	_, _ = crand.Read(sfx[:])
	p.StaPhy += fmt.Sprintf("%02x%02x", sfx[0], sfx[1])
	p.APPhy += fmt.Sprintf("%02x%02x", sfx[0], sfx[1])
	if err := os.WriteFile(filepath.Join(p.Dir, "radios"), []byte(p.StaPhy+" "+p.APPhy+"\n"), 0o600); err != nil {
		return err
	}
	// 网卡已经搬进手机后才失败的话，在手机的网络命名空间里把它删掉（不然手机里留一块没用的 wlan0）
	movedToPhone := false
	defer func() {
		if err != nil && movedToPhone {
			_ = DelRadioFunc("/proc/"+pid+"/ns/net", p.StaPhy)
		}
	}()
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
	movedToPhone = true
	// 手机里看到的网卡名（/sys/class/ieee80211、/sys/class/net/wlan0/phy80211/name）改成真机那样的 phyN（踩坑 #76：
	// 原来是 ppsc3406…，一看就是我们建的）。wiphy 名字全内核唯一，phy0 被别的手机占了就往后试；改不成就留原名，不影响 WiFi。
	if name := renamePhy(ctx, run, pid); name != "" {
		p.StaPhy = name
		_ = os.WriteFile(filepath.Join(p.Dir, "radios"), []byte(p.StaPhy+" "+p.APPhy+"\n"), 0o600)
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
	if err := os.WriteFile(filepath.Join(p.Dir, "hostapd.conf"), []byte(p.HostapdConf()), 0o600); err != nil {
		return err
	}
	if err := startDaemons(ctx, run, p); err != nil {
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

// 热点和 DHCP 程序：有 systemd 时各起一个临时服务（systemd-run）。
// 不能当管家的子进程跑：管家一重启（自动更新），systemd 会把管家服务里的进程全杀掉，手机就断 WiFi 了（踩坑 #57）。
// 临时服务不受管家重启影响，挂了还会自动拉起来。没有 systemd 就退回后台进程（-B），靠 EnsureDaemons 补。

// useSystemd 宿主机用 systemd 吗（测试时换掉）。
var useSystemd = func() bool {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	_, err := exec.LookPath("systemd-run")
	return err == nil
}

func unitName(p Plan, what string) string {
	return "ppwifi-" + strings.TrimPrefix(p.Netns, "ppr-") + "-" + what
}

func hostapdArgs(p Plan, background bool) []string {
	a := []string{"hostapd"}
	if background {
		a = append(a, "-B", "-P", filepath.Join(p.Dir, "hostapd.pid"))
	}
	return append(a, "-f", filepath.Join(p.Dir, "hostapd.log"), filepath.Join(p.Dir, "hostapd.conf"))
}

func dnsmasqArgs(p Plan, foreground bool) []string {
	a := []string{"dnsmasq", "--interface=wlan1", "--bind-interfaces", "--except-interface=lo",
		fmt.Sprintf("--dhcp-range=192.168.%d.100,192.168.%d.199,12h", p.Subnet, p.Subnet),
		fmt.Sprintf("--dhcp-host=%s,%s", macString(p.StaMAC), p.PhoneIP()),
		"--dhcp-option=6," + p.RouterIP(), "--port=0",
		"--pid-file=" + filepath.Join(p.Dir, "dnsmasq.pid"), "--dhcp-leasefile=" + filepath.Join(p.Dir, "leases")}
	if foreground {
		a = append(a, "--keep-in-foreground")
	}
	return a
}

// startDaemons 起热点和 DHCP（已在跑的先停掉）。
func startDaemons(ctx context.Context, run Runner, p Plan) error {
	in := func(args ...string) []string { return append([]string{"netns", "exec", p.Netns}, args...) }
	sd := useSystemd()
	for _, d := range []struct {
		what, name string
		args       []string
	}{
		{"热点（hostapd）", "hostapd", hostapdArgs(p, !sd)},
		{"DHCP（dnsmasq）", "dnsmasq", dnsmasqArgs(p, sd)},
	} {
		var out string
		var err error
		if sd {
			u := unitName(p, d.name)
			_, _ = run.Run(ctx, "systemctl", "stop", u)
			_, _ = run.Run(ctx, "systemctl", "reset-failed", u)
			full := append([]string{"--unit=" + u, "--collect", "--quiet", "-p", "Restart=always", "-p", "RestartSec=2",
				"--description=prism-phone 手机 WiFi " + d.name, "ip"}, in(d.args...)...)
			out, err = run.Run(ctx, "systemd-run", full...)
		} else {
			out, err = run.Run(ctx, "ip", in(d.args...)...)
		}
		if err != nil {
			return fmt.Errorf("%s：%v %s", d.what, err, strings.TrimSpace(out))
		}
	}
	if sd {
		// 前台跑的 hostapd 配置错了会马上退出：等一下确认还在
		time.Sleep(1500 * time.Millisecond)
		if out, err := run.Run(ctx, "systemctl", "is-active", unitName(p, "hostapd")); err != nil || strings.TrimSpace(out) != "active" {
			log, _ := os.ReadFile(filepath.Join(p.Dir, "hostapd.log"))
			return fmt.Errorf("热点（hostapd）没起来：%s", lastLines(string(log), 3))
		}
	}
	return nil
}

// EnsureDaemons 路由器还在、但热点或 DHCP 没在跑（比如以前的版本随管家重启被杀了）就重新拉起来。返回是否补过。
func EnsureDaemons(ctx context.Context, run Runner, p Plan) (bool, error) {
	if _, err := os.Stat("/run/netns/" + p.Netns); err != nil {
		return false, nil
	}
	if alive(ctx, run, p, "hostapd") && alive(ctx, run, p, "dnsmasq") {
		return false, nil
	}
	for _, f := range []string{"hostapd.pid", "dnsmasq.pid"} {
		if b, err := os.ReadFile(filepath.Join(p.Dir, f)); err == nil {
			if pid := strings.TrimSpace(string(b)); pid != "" {
				_, _ = run.Run(ctx, "kill", pid)
			}
		}
	}
	return true, startDaemons(ctx, run, p)
}

func alive(ctx context.Context, run Runner, p Plan, name string) bool {
	if useSystemd() {
		out, err := run.Run(ctx, "systemctl", "is-active", unitName(p, name))
		if err == nil && strings.TrimSpace(out) == "active" {
			return true
		}
	}
	b, err := os.ReadFile(filepath.Join(p.Dir, name+".pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " | ")
}

// SysIeee80211 宿主机上无线网卡（wiphy）的列表目录（测试时换掉）。
var SysIeee80211 = "/sys/class/ieee80211"

// DelRadioFunc 删 hwsim 网卡（测试时换成假的）。
var DelRadioFunc = DelRadioInNetns

// Teardown 停掉热点 / DHCP，删掉这台的两块 hwsim 网卡和路由器网络命名空间。没有就什么都不做。
// 实测删命名空间不会自动删网卡（会一直留着），所以要显式删：路由器那块在路由器命名空间里删；
// 手机那块在手机关机后回到管家所在的命名空间，在这里删。
func Teardown(ctx context.Context, run Runner, p Plan) error {
	// 这次开机用的网卡名（带随机后缀，见 Setup）
	names := []string{p.APPhy, p.StaPhy}
	if b, err := os.ReadFile(filepath.Join(p.Dir, "radios")); err == nil {
		names = append(names, strings.Fields(string(b))...)
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		_ = DelRadioFunc("/run/netns/"+p.Netns, n)
		_ = DelRadioFunc("", n)
	}
	// 以前漏掉的（异步还回宿主机、当时没删成的）：宿主机上凡是这台手机前缀的网卡都删
	if p.StaPhy != "" && p.APPhy != "" {
		if ents, err := os.ReadDir(SysIeee80211); err == nil {
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), p.StaPhy) || strings.HasPrefix(e.Name(), p.APPhy) {
					_ = DelRadioFunc("", e.Name())
				}
			}
		}
	}
	_ = os.Remove(filepath.Join(p.Dir, "radios"))
	if useSystemd() {
		_, _ = run.Run(ctx, "systemctl", "stop", unitName(p, "hostapd"), unitName(p, "dnsmasq"))
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
	// 魔云腾 Q1 上用的是改了名的同一个驱动 wlan.ko（模块名不带 hwsim，踩坑 #134）
	_, errW := os.Stat("/sys/module/wlan/parameters/radios")
	if _, err := os.Stat("/sys/module/mac80211_hwsim"); err != nil && errW != nil {
		if out, err := run.Run(ctx, "modprobe", "mac80211_hwsim", "radios=0"); err != nil {
			return fmt.Errorf("内核没有 mac80211_hwsim：%v %s", err, out)
		}
	}
	// WPA2 热点要 AES-CCMP（ccm 模块）。有的内核不会自动加载，缺了 hostapd 报 "key addition failed"（踩坑 #57）
	if _, err := os.Stat("/sys/module/ccm"); err != nil && !cryptoHas("ccm(aes)") {
		if out, err := run.Run(ctx, "modprobe", "ccm"); err != nil {
			return fmt.Errorf("加载 ccm（WPA2 加密）失败：%v %s", err, out)
		}
	}
	return nil
}

// renamePhy 在手机的网络命名空间里把 wlan0 所在的无线网卡改名成第一个空着的 phyN，返回新名字（失败返回 ""）。
// 按 wiphy 编号（phy#N）操作：iw 按名字找要读 /sys，从外面看不到手机里的网卡。
func renamePhy(ctx context.Context, run Runner, pid string) string {
	out, err := run.Run(ctx, "nsenter", "-t", pid, "-n", "iw", "dev", "wlan0", "info")
	if err != nil {
		return ""
	}
	idx := ""
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "wiphy" {
			idx = f[1]
		}
	}
	if idx == "" {
		return ""
	}
	for i := 0; i < 64; i++ {
		name := fmt.Sprintf("phy%d", i)
		if _, err := run.Run(ctx, "nsenter", "-t", pid, "-n", "iw", "phy#"+idx, "set", "name", name); err == nil {
			return name
		}
	}
	return ""
}

// RestorePhyName 关机前把手机里无线网卡的名字从 phy0 改回建它时的名字（pps…）：网卡回到宿主机后
// 不和别的手机还回来的 phy0 撞名（宿主机里 phyN 只许等于自己的编号，撞名会改不回、内核告警，踩坑 #134）。
// 失败不要紧（Teardown 也按 phy0 删）。
func RestorePhyName(ctx context.Context, run Runner, pid, name string) {
	if name == "" || pid == "" || pid == "0" {
		return
	}
	out, err := run.Run(ctx, "nsenter", "-t", pid, "-n", "iw", "dev", "wlan0", "info")
	if err != nil {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "wiphy" {
			_, _ = run.Run(ctx, "nsenter", "-t", pid, "-n", "iw", "phy#"+f[1], "set", "name", name)
			return
		}
	}
}

// cryptoProc 内核算法表（测试时换掉）。
var cryptoProc = "/proc/crypto"

// cryptoHas 内核里已经有这个算法（编进内核的不会出现在 /sys/module 下，例如魔云腾 Q1 的 CCM，踩坑 #129）。
func cryptoHas(name string) bool {
	b, err := os.ReadFile(cryptoProc)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.SplitN(l, ":", 2); len(f) == 2 && strings.TrimSpace(f[0]) == "name" && strings.TrimSpace(f[1]) == name {
			return true
		}
	}
	return false
}
