// Package network 生成 mihomo（Clash 内核）的配置：每台云手机的流量走它自己的代理。
//
// 做法：宿主机用 TPROXY 把容器网段（只是容器网段，宿主机自己的 SSH、DC 不受影响）的流量交给 mihomo，
// mihomo 按来源 IP（每台云手机固定一个容器 IP）分到对应代理。
// 规矩：
//   - 没有代理、代理不认识的设备 → REJECT，绝不走宿主机自己的网络（DIRECT 只给开发机的「直连」测试用）。
//   - 代理断了 → 连接失败，不切换到别的出口。
//   - DNS 也经 mihomo，按同样的规则走代理解析，不用宿主机的 DNS。
package network

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// DirectDevProxyID 开发机的「直连（仅开发）」（和 phones.DirectDevProxyID 同一个值）。
const DirectDevProxyID = "direct-dev"

// Options 宿主机这边的参数。
type Options struct {
	TProxyPort int    // mihomo 收透明代理流量的端口
	DNSListen  string // mihomo DNS 监听地址，例如 10.88.0.1:1053
	Subnet     string // 容器网段，例如 10.88.0.0/16
	DevHost    bool   // 开发机才允许「直连（仅开发）」
	APIListen  string // mihomo 控制接口（只监听本机），空 = 不开
	// CheckPort 检测入口：只监听 127.0.0.1 的本地代理端口，背后是可切换的 pp-check 分组，
	// 管家程序检测 vless / trojan 等协议时把它切到要测的节点。0 = 不开。
	CheckPort int
	// Redir 用 REDIRECT(NAT)模式而不是 TPROXY（有的板子 vendor 内核 tproxy 投递不进 mihomo，踩坑 #101）。
	// 只代理 TCP；UDP（除 DNS）由 nft 规则 drop，逼 QUIC 回退 TCP。
	Redir bool
}

// CheckGroup 检测入口背后的分组名。
const CheckGroup = "pp-check"

// Result 生成的配置，以及哪些设备没能分到出口（会被拦截，管理页要提示）。
type Result struct {
	YAML    string
	Blocked map[string]string // 设备 ID → 原因
}

// outbound 一个代理在 mihomo 里的写法（键值和 mihomo 配置文件一致）。
func outbound(px store.Proxy) (map[string]any, error) {
	name := "px-" + px.ID
	if px.Protocol == "subscription" {
		return nil, fmt.Errorf("这是订阅链接，请在代理库里「导入订阅」拆成单个节点后再分配")
	}
	if px.Protocol == "clash" {
		// 从 Clash 订阅导入的节点：地址里存的是 mihomo 原样的节点配置（JSON）。
		var out map[string]any
		if err := json.Unmarshal([]byte(px.Address), &out); err != nil || out["server"] == nil {
			return nil, fmt.Errorf("节点配置不对")
		}
		out["name"] = name
		return out, nil
	}
	addr := strings.TrimSpace(px.Address)
	if !strings.Contains(addr, "://") {
		addr = px.Protocol + "://" + addr
	}
	scheme, _, _ := strings.Cut(addr, "://")
	var out map[string]any
	switch strings.ToLower(scheme) {
	case "socks5", "socks5h", "socks", "http", "https":
		u, err := url.Parse(addr)
		if err != nil || u.Hostname() == "" || portOK(u.Port()) != nil {
			return nil, fmt.Errorf("地址格式不对")
		}
		out = map[string]any{"name": name, "server": u.Hostname(), "port": u.Port()}
		switch strings.ToLower(u.Scheme) {
		case "http":
			out["type"] = "http"
		case "https":
			out["type"] = "http"
			out["tls"] = true
		default:
			out["type"] = "socks5"
			out["udp"] = true
		}
		if u.User != nil {
			out["username"] = u.User.Username()
			if p, ok := u.User.Password(); ok {
				out["password"] = p
			}
		}
	default:
		ob, err := linkOutbound(name, addr)
		if err != nil {
			return nil, err
		}
		out = ob
	}
	port, _ := strconv.Atoi(fmt.Sprint(out["port"]))
	out["port"] = port
	return out, nil
}

// Build 按当前的设备和代理生成 mihomo 配置。
func Build(st store.State, opt Options) Result {
	res := Result{Blocked: map[string]string{}}
	proxies := map[string]store.Proxy{}
	for _, px := range st.Proxies {
		proxies[px.ID] = px
	}
	used := map[string]map[string]any{}
	var rules []string
	phones := append([]store.Phone(nil), st.Phones...)
	sort.Slice(phones, func(i, j int) bool { return phones[i].IP < phones[j].IP })
	for _, p := range phones {
		if p.IP == "" {
			res.Blocked[p.ID] = "没有容器 IP"
			continue
		}
		src := "SRC-IP-CIDR," + p.IP + "/32,"
		switch {
		case p.ProxyID == DirectDevProxyID && opt.DevHost:
			rules = append(rules, src+"DIRECT")
		case p.ProxyID == "":
			res.Blocked[p.ID] = "没有分配代理"
		default:
			px, ok := proxies[p.ProxyID]
			if !ok {
				res.Blocked[p.ID] = "代理已从代理库删除"
				continue
			}
			ob, err := outbound(px)
			if err != nil {
				res.Blocked[p.ID] = "代理「" + px.Name + "」" + err.Error()
				continue
			}
			used[px.ID] = ob
			rules = append(rules, src+ob["name"].(string))
		}
	}
	// 其余所有流量（包括没分到出口的设备）一律拒绝。
	rules = append(rules, "MATCH,REJECT")

	// 检测入口：代理库里所有能用的节点都放进配置，供 pp-check 分组切换（不影响任何设备的规则）。
	var checkable []string
	if opt.CheckPort > 0 {
		for _, px := range st.Proxies {
			if _, ok := used[px.ID]; !ok {
				if ob, err := outbound(px); err == nil {
					used[px.ID] = ob
				}
			}
			if ob, ok := used[px.ID]; ok {
				checkable = append(checkable, ob["name"].(string))
			}
		}
		sort.Strings(checkable)
	}

	var b strings.Builder
	b.WriteString("# 管家程序自动生成，不要手改（改了也会被覆盖）\n")
	if opt.Redir {
		fmt.Fprintf(&b, "redir-port: %d\n", opt.TProxyPort) // REDIRECT(NAT)模式，内核 tproxy 不行时用（踩坑 #101）
	} else {
		fmt.Fprintf(&b, "tproxy-port: %d\n", opt.TProxyPort)
	}
	b.WriteString("allow-lan: true\nbind-address: \"*\"\nmode: rule\nlog-level: warning\nipv6: false\n")
	if opt.APIListen != "" {
		fmt.Fprintf(&b, "external-controller: %s\n", yamlString(opt.APIListen))
	}
	b.WriteString("profile:\n  store-selected: false\n  store-fake-ip: false\n")
	b.WriteString("dns:\n  enable: true\n")
	fmt.Fprintf(&b, "  listen: %s\n", yamlString(opt.DNSListen))
	// 2026-11-17 改：以前用 fake-ip，App 解析任何域名都拿到 198.18.x.x（Clash 假地址段，一看就是开了代理，踩坑 #73）。
	// 现在手机拿到真实的公网 IP（DoH 查的，不走宿主机系统 DNS）；连接时下面的 sniffer 从 TLS / HTTP / QUIC 里读出域名，
	// 交给节点按域名去连（节点那头解析）——访问的服务器还是节点当地的，和以前 fake-ip 效果一样。
	b.WriteString("  ipv6: false\n  enhanced-mode: redir-host\n")
	b.WriteString("  default-nameserver: [1.1.1.1, 8.8.8.8]\n  nameserver: [\"https://1.1.1.1/dns-query\"]\n")
	// 解析「节点服务器自己的域名」用宿主机系统 DNS（踩坑 #123）：和宿主机本身连节点的效果一致。
	// 板子在家里的网络下，路由器（iStoreOS + OpenClash 假 IP 模式）会拦所有 DNS：被墙的节点域名拿到 198.18 假地址，
	// 连过去由路由器代理转发——家里别的设备能用这些节点就是靠这个。mihomo 原来用 1.1.1.1 / 阿里 DoH 拿到真实 IP，
	// 国内直连不通 → 5 个节点检测失败。没有这种路由器的地方，系统 DNS 照样正常解析。只影响「去哪连节点」。
	b.WriteString("  proxy-server-nameserver: [system]\n")
	b.WriteString("sniffer:\n  enable: true\n  force-dns-mapping: true\n  parse-pure-ip: true\n  override-destination: true\n")
	b.WriteString("  sniff:\n    HTTP: {ports: [80, 8080-8880], override-destination: true}\n    TLS: {ports: [443, 8443]}\n    QUIC: {ports: [443, 8443]}\n")
	ids := make([]string, 0, len(used))
	for id := range used {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b.WriteString("proxies:\n")
	if len(ids) == 0 {
		b.WriteString("  []\n")
	}
	for _, id := range ids {
		// 每个代理写成一行 JSON（JSON 也是合法的 YAML），密码、路径里有特殊字符也不会写坏。
		line, err := json.Marshal(used[id])
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "  - %s\n", line)
	}
	if opt.CheckPort > 0 {
		group, _ := json.Marshal(map[string]any{"name": CheckGroup, "type": "select", "proxies": append([]string{"REJECT"}, checkable...)})
		fmt.Fprintf(&b, "proxy-groups:\n  - %s\n", group)
		fmt.Fprintf(&b, "listeners:\n  - {name: pp-check-in, type: mixed, listen: 127.0.0.1, port: %d, proxy: %s}\n", opt.CheckPort, CheckGroup)
	}
	b.WriteString("rules:\n")
	for _, r := range rules {
		fmt.Fprintf(&b, "  - %s\n", r)
	}
	res.YAML = b.String()
	return res
}

// yamlString 生成安全的 YAML 字符串（双引号转义）。
func yamlString(s string) string {
	return strconv.Quote(s)
}
