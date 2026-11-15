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
var supportedClashTypes = map[string]bool{
	"ss": true, "vmess": true, "vless": true, "trojan": true, "hysteria2": true, "socks5": true, "http": true, "tuic": true,
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
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, nil, fmt.Errorf("Clash 订阅格式不对：%v", err)
	}
	var nodes []Node
	var skipped []string
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
	if len(nodes) == 0 {
		return nil, skipped, fmt.Errorf("订阅里没有能用的节点")
	}
	return nodes, skipped, nil
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
