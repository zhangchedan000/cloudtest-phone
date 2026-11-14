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
}

// Result 生成的配置，以及哪些设备没能分到出口（会被拦截，管理页要提示）。
type Result struct {
	YAML    string
	Blocked map[string]string // 设备 ID → 原因
}

// outbound 一个代理在 mihomo 里的写法（键值和 mihomo 配置文件一致）。
func outbound(px store.Proxy) (map[string]any, error) {
	name := "px-" + px.ID
	if px.Protocol == "subscription" {
		return nil, fmt.Errorf("订阅链接还没接入，请先添加单个节点")
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

	var b strings.Builder
	b.WriteString("# 管家程序自动生成，不要手改（改了也会被覆盖）\n")
	fmt.Fprintf(&b, "tproxy-port: %d\n", opt.TProxyPort)
	b.WriteString("allow-lan: true\nbind-address: \"*\"\nmode: rule\nlog-level: warning\nipv6: false\n")
	if opt.APIListen != "" {
		fmt.Fprintf(&b, "external-controller: %s\n", yamlString(opt.APIListen))
	}
	b.WriteString("profile:\n  store-selected: false\n  store-fake-ip: false\n")
	b.WriteString("dns:\n  enable: true\n")
	fmt.Fprintf(&b, "  listen: %s\n", yamlString(opt.DNSListen))
	b.WriteString("  ipv6: false\n  enhanced-mode: fake-ip\n  fake-ip-range: 198.18.0.1/16\n")
	// fake-ip：手机拿到假地址，真正的域名解析在代理那头做，不经宿主机的 DNS。
	b.WriteString("  default-nameserver: [1.1.1.1, 8.8.8.8]\n  nameserver: [\"https://1.1.1.1/dns-query\"]\n")
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
