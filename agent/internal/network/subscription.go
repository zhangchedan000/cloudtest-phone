package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 订阅：把订阅里的节点拆成代理库里的一条条代理（每台云手机固定用其中一个节点）。
// 支持两种常见格式：Clash YAML（有 proxies:）、base64 / 明文的分享链接列表（一行一个）。

// Node 订阅里的一个节点，已经转成代理库的样子（还没分配 ID）。
type Node struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
}

// supportedClashTypes mihomo 支持、我们也愿意放进配置的节点类型。
// anytls 需要 mihomo ≥ v1.19（阿什本 v1.19.34 已验证 mihomo -t 通过），很多机场的住宅/家宽节点是这个。
var supportedClashTypes = map[string]bool{
	"ss": true, "vmess": true, "vless": true, "trojan": true, "hysteria2": true, "socks5": true, "http": true, "tuic": true, "anytls": true,
}

// ParseSubscription 解析订阅内容；认不出的节点跳过并说明原因。
func ParseSubscription(body string) (nodes []Node, skipped []string, err error) {
	text := strings.TrimSpace(body)
	if text == "" {
		return nil, nil, fmt.Errorf("订阅内容是空的")
	}
	if strings.Contains(text, "proxies:") {
		return parseClash(text)
	}
	if !strings.Contains(text, "://") {
		decoded, derr := b64(text)
		if derr != nil {
			return nil, nil, fmt.Errorf("认不出订阅格式（既不是 Clash YAML，也不是分享链接列表）")
		}
		text = decoded
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		scheme, _, ok := strings.Cut(line, "://")
		if !ok {
			skipped = append(skipped, "不是链接："+short(line))
			continue
		}
		if _, err := linkOutbound("check", line); err != nil {
			skipped = append(skipped, short(line)+"："+err.Error())
			continue
		}
		nodes = append(nodes, Node{Name: linkName(line), Protocol: strings.ToLower(scheme), Address: line})
	}
	if len(nodes) == 0 {
		return nil, skipped, fmt.Errorf("订阅里没有能用的节点")
	}
	return nodes, skipped, nil
}

func parseClash(text string) ([]Node, []string, error) {
	nodes, skipped := parseClashNodes(text)
	if len(nodes) == 0 {
		return nil, skipped, fmt.Errorf("订阅里没有能用的节点")
	}
	return nodes, skipped, nil
}

// parseClashNodes 解析 Clash 配置顶层 proxies，空列表不当错误（可能节点挂在 proxy-providers 里）。
func parseClashNodes(text string) (nodes []Node, skipped []string) {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, nil
	}
	for _, p := range doc.Proxies {
		name := strings.TrimSpace(fmt.Sprint(p["name"]))
		typ := strings.ToLower(fmt.Sprint(p["type"]))
		if !supportedClashTypes[typ] {
			skipped = append(skipped, fmt.Sprintf("%s：%s 类型不支持", name, typ))
			continue
		}
		if fmt.Sprint(p["server"]) == "" || p["port"] == nil {
			skipped = append(skipped, name+"：缺地址或端口")
			continue
		}
		raw, err := json.Marshal(p)
		if err != nil {
			skipped = append(skipped, name+"：内容不对")
			continue
		}
		nodes = append(nodes, Node{Name: name, Protocol: "clash", Address: string(raw)})
	}
	return nodes, skipped
}

// clashProviderURLs 取出 Clash 配置里 http 类型 proxy-provider 的下载地址。
// 很多机场（如 OpenClash 订阅）顶层 proxies 是空的，真正的节点挂在这些二级地址上。
func clashProviderURLs(text string) []string {
	if !strings.Contains(text, "proxy-providers:") {
		return nil
	}
	var doc struct {
		ProxyProviders map[string]struct {
			Type string `yaml:"type"`
			URL  string `yaml:"url"`
		} `yaml:"proxy-providers"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil
	}
	var urls []string
	for _, pv := range doc.ProxyProviders {
		if strings.EqualFold(strings.TrimSpace(pv.Type), "http") && strings.TrimSpace(pv.URL) != "" {
			urls = append(urls, strings.TrimSpace(pv.URL))
		}
	}
	return urls
}

// FetchFunc 下载一个订阅 / provider 地址的内容（实参一般是 FetchSubscription）。
type FetchFunc func(ctx context.Context, url string) (string, error)

// ParseSubscriptionWithProviders 在 ParseSubscription 的基础上，自动跟进 Clash 配置里的
// proxy-providers：顶层没节点时（机场常把节点挂在 provider 的二级地址上），逐个下载 provider
// 再解析，合并去重。fetch 为 nil 或订阅里没有 provider 时，行为和 ParseSubscription 一致。
func ParseSubscriptionWithProviders(ctx context.Context, body string, fetch FetchFunc) (nodes []Node, skipped []string, err error) {
	text := strings.TrimSpace(body)
	if text == "" {
		return nil, nil, fmt.Errorf("订阅内容是空的")
	}
	providers := clashProviderURLs(text)
	// 先尽量收顶层节点。顶层没节点但有 provider 是正常的，继续跟进 provider。
	if n, s, e := ParseSubscription(text); e == nil {
		nodes, skipped = n, s
	} else if len(providers) == 0 {
		return nil, nil, e
	}
	// 跟进 proxy-providers（逐个下载再解析）。
	if fetch != nil {
		seen := map[string]bool{}
		for _, u := range providers {
			if seen[u] {
				continue
			}
			seen[u] = true
			pb, fe := fetch(ctx, u)
			if fe != nil {
				skipped = append(skipped, "provider "+short(u)+"："+fe.Error())
				continue
			}
			pn, ps, pe := ParseSubscription(pb)
			skipped = append(skipped, ps...)
			if pe != nil {
				skipped = append(skipped, "provider "+short(u)+"："+pe.Error())
				continue
			}
			nodes = append(nodes, pn...)
		}
	}
	nodes = dedupeNodes(nodes)
	if len(nodes) == 0 {
		return nil, skipped, fmt.Errorf("订阅里没有能用的节点")
	}
	return nodes, skipped, nil
}

// dedupeNodes 按地址去重（同一节点可能既在顶层又在 provider 里）。
func dedupeNodes(in []Node) []Node {
	seen := map[string]bool{}
	out := make([]Node, 0, len(in))
	for _, n := range in {
		if strings.TrimSpace(n.Address) == "" || seen[n.Address] {
			continue
		}
		seen[n.Address] = true
		out = append(out, n)
	}
	return out
}

// linkName 分享链接 # 后面是节点名（vmess 的名字在 JSON 的 ps 里）。
func linkName(link string) string {
	if strings.HasPrefix(strings.ToLower(link), "vmess://") {
		if raw, err := b64(strings.TrimPrefix(link, link[:8])); err == nil {
			var v map[string]any
			if json.Unmarshal([]byte(raw), &v) == nil && v["ps"] != nil {
				return fmt.Sprint(v["ps"])
			}
		}
	}
	if _, frag, ok := strings.Cut(link, "#"); ok {
		if name, err := urlUnescape(frag); err == nil && name != "" {
			return name
		}
	}
	return short(link)
}

func short(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// FetchSubscription 下载订阅。带 clash 的 User-Agent，很多机场会因此返回 Clash YAML（节点信息更全）。
func FetchSubscription(ctx context.Context, url string) (string, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return "", fmt.Errorf("订阅地址要以 http:// 或 https:// 开头")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("订阅地址不对")
	}
	req.Header.Set("User-Agent", "clash.meta")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("下载订阅失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载订阅失败：服务器返回 %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", fmt.Errorf("下载订阅失败：%v", err)
	}
	return string(b), nil
}

// ImportNodes 把节点加进代理库：同样地址的跳过；返回新加的代理。
// newID 给出想用的 ID，已经被占用时自动加后缀（同一秒导入两次不会撞 ID）。
func ImportNodes(st *store.State, nodes []Node, note string, newID func(int) string) []store.Proxy {
	existing := map[string]bool{}
	for _, p := range st.Proxies {
		existing[p.Address] = true
	}
	var added []store.Proxy
	for i, n := range nodes {
		if existing[n.Address] {
			continue
		}
		existing[n.Address] = true
		px := store.Proxy{ID: st.FreeID(newID(i)), Name: n.Name, Protocol: n.Protocol, Address: n.Address, Note: note}
		st.Proxies = append(st.Proxies, px)
		added = append(added, px)
	}
	return added
}
