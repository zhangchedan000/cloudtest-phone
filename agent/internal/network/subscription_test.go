package network

import (
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
