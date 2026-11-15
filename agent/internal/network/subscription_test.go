package network

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

const clashSub = `
proxies:
  - {name: "美国 01", type: ss, server: 1.2.3.4, port: 8388, cipher: aes-256-gcm, password: "p:w"}
  - name: 美国 02
    type: vless
    server: us2.example.com
    port: 443
    uuid: 11111111-2222
    tls: true
  - {name: 旧协议, type: ssr, server: 5.6.7.8, port: 1}
proxy-groups: []
`

func TestParseClashSubscription(t *testing.T) {
	nodes, skipped, err := ParseSubscription(clashSub)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Name != "美国 01" || nodes[0].Protocol != "clash" || len(skipped) != 1 || !strings.Contains(skipped[0], "ssr") {
		t.Fatalf("解析不对：%+v %v", nodes, skipped)
	}
	// 导入后能进 mihomo 配置
	st := store.State{Phones: []store.Phone{{ID: "a", IP: "10.88.0.10", ProxyID: "n0"}}}
	added := ImportNodes(&st, nodes, "订阅", func(i int) string { return fmt.Sprintf("n%d", i) })
	if len(added) != 2 {
		t.Fatalf("应新增 2 条：%d", len(added))
	}
	y := Build(st, Options{TProxyPort: 7893, DNSListen: "x"}).YAML
	if !strings.Contains(y, `"name":"px-n0"`) || !strings.Contains(y, `"cipher":"aes-256-gcm"`) || !strings.Contains(y, "10.88.0.10/32,px-n0") {
		t.Fatalf("配置不对：\n%s", y)
	}
	if again := ImportNodes(&st, nodes, "订阅", func(i int) string { return fmt.Sprintf("m%d", i) }); len(again) != 0 {
		t.Fatal("重复导入应跳过")
	}
}

// 同一秒导入两次（想用的 ID 一样）不能撞 ID：撞了 mihomo 里会有两个同名出口，检测和分配都会落到别的节点上。
func TestImportNodesUniqueIDs(t *testing.T) {
	st := store.State{Groups: []store.Group{{ID: "p-1-000-2"}}}
	same := func(i int) string { return fmt.Sprintf("p-1-%03d", i) }
	a := ImportNodes(&st, []Node{{Name: "a", Protocol: "ss", Address: "ss://a"}}, "", same)
	b := ImportNodes(&st, []Node{{Name: "b", Protocol: "ss", Address: "ss://b"}}, "", same)
	c := ImportNodes(&st, []Node{{Name: "c", Protocol: "ss", Address: "ss://c"}}, "", same)
	if a[0].ID != "p-1-000" || b[0].ID != "p-1-000-3" || c[0].ID != "p-1-000-4" {
		t.Fatalf("ID 不对：%s %s %s", a[0].ID, b[0].ID, c[0].ID)
	}
	seen := map[string]bool{}
	for _, p := range st.Proxies {
		if seen[p.ID] {
			t.Fatalf("ID 重复：%s", p.ID)
		}
		seen[p.ID] = true
	}
}

// 机场订阅顶层 proxies 空、节点挂在 proxy-providers 二级地址上（OpenClash 这类），
// 导入要自动跟进 provider 把节点取回来。
func TestParseSubscriptionFollowsProviders(t *testing.T) {
	top := `
proxies: []
proxy-providers:
  airport:
    type: http
    url: https://sub.example.com/provider
    interval: 3600
  file-based:
    type: file
    path: /tmp/x.yaml
proxy-groups: []
`
	providerBody := `
proxies:
  - {name: "US-vless", type: vless, server: us.example.com, port: 443, uuid: aaaa-bbbb, tls: true}
  - {name: "HK-anytls", type: anytls, server: hk.example.com, port: 443, password: p}
`
	fetch := func(_ context.Context, url string) (string, error) {
		if url != "https://sub.example.com/provider" {
			return "", fmt.Errorf("不该下载这个地址：%s", url)
		}
		return providerBody, nil
	}
	nodes, skipped, err := ParseSubscriptionWithProviders(context.Background(), top, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "US-vless" {
		t.Fatalf("应从 provider 取回 1 个 vless 节点：%+v", nodes)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "anytls") {
		t.Fatalf("anytls 应被跳过并说明：%v", skipped)
	}
	// 没有 fetch 回退到纯解析：顶层空 + 无法下载 provider => 报没节点。
	if _, _, err := ParseSubscriptionWithProviders(context.Background(), top, nil); err == nil {
		t.Fatal("没有 fetch 时顶层空订阅应报错")
	}
	// 顶层就有节点的普通订阅，行为和 ParseSubscription 一致。
	n, _, err := ParseSubscriptionWithProviders(context.Background(), clashSub, fetch)
	if err != nil || len(n) != 2 {
		t.Fatalf("普通 Clash 订阅应正常解析：%+v %v", n, err)
	}
}

func TestParseLinkListSubscription(t *testing.T) {
	list := "trojan://pw@tj.example.com:443#%E7%BE%8E%E5%9B%BD%20TJ\nss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:x@1.1.1.1:1")) + "#SS\nfoo://bar\n"
	nodes, skipped, err := ParseSubscription(base64.StdEncoding.EncodeToString([]byte(list)))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Name != "美国 TJ" || nodes[1].Protocol != "ss" || len(skipped) != 1 {
		t.Fatalf("解析不对：%+v %v", nodes, skipped)
	}
	if _, _, err := ParseSubscription("hello"); err == nil {
		t.Fatal("认不出的内容应报错")
	}
}
