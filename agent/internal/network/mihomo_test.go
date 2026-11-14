package network

import (
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

func state() store.State {
	return store.State{
		Proxies: []store.Proxy{
			{ID: "p1", Name: "达拉斯", Protocol: "socks5", Address: `socks5://user:p%22a%3Ass@1.2.3.4:1080`},
			{ID: "p2", Name: "纽约", Protocol: "http", Address: "5.6.7.8:3128"},
			{ID: "p3", Name: "订阅", Protocol: "subscription", Address: "https://sub.example.com/clash"},
		},
		Phones: []store.Phone{
			{ID: "a", IP: "10.88.0.10", ProxyID: "p1"},
			{ID: "b", IP: "10.88.0.11", ProxyID: "p2"},
			{ID: "c", IP: "10.88.0.12", ProxyID: "p3"},
			{ID: "d", IP: "10.88.0.13", ProxyID: ""},
			{ID: "e", IP: "10.88.0.14", ProxyID: "gone"},
			{ID: "f", IP: "10.88.0.15", ProxyID: DirectDevProxyID},
		},
	}
}

func TestEachPhoneGoesThroughItsOwnProxy(t *testing.T) {
	res := Build(state(), Options{TProxyPort: 7893, DNSListen: "10.88.0.1:1053", Subnet: "10.88.0.0/16"})
	y := res.YAML
	for _, want := range []string{
		"SRC-IP-CIDR,10.88.0.10/32,px-p1", "SRC-IP-CIDR,10.88.0.11/32,px-p2", "MATCH,REJECT",
		`"password":"p\"a:ss"`, `"type":"socks5"`, `"type":"http"`, `"server":"5.6.7.8"`, `"port":1080`,
	} {
		if !strings.Contains(y, want) {
			t.Fatalf("配置里缺 %q：\n%s", want, y)
		}
	}
	if strings.HasSuffix(strings.TrimSpace(y), "DIRECT") || strings.Contains(y, "10.88.0.15/32,DIRECT") {
		t.Fatal("正式机器不允许直连")
	}
	for id, reason := range map[string]string{"c": "订阅链接还没接入", "d": "没有分配代理", "e": "删除", "f": ""} {
		got, blocked := res.Blocked[id]
		if reason == "" {
			continue
		}
		if !blocked || !strings.Contains(got, reason) {
			t.Fatalf("设备 %s 应被拦截（%s），得到 %q", id, reason, got)
		}
	}
	if strings.Contains(y, "10.88.0.12") || strings.Contains(y, "10.88.0.13") {
		t.Fatal("没出口的设备不应有放行规则（落到 MATCH,REJECT）")
	}
}

func TestDevHostAllowsDirect(t *testing.T) {
	res := Build(state(), Options{TProxyPort: 7893, DNSListen: "10.88.0.1:1053", DevHost: true})
	if !strings.Contains(res.YAML, "SRC-IP-CIDR,10.88.0.15/32,DIRECT") {
		t.Fatal("开发机的直连设备应放行")
	}
}

func TestNoProxiesStillValid(t *testing.T) {
	res := Build(store.State{}, Options{TProxyPort: 7893, DNSListen: "10.88.0.1:1053"})
	if !strings.Contains(res.YAML, "proxies:\n  []") || !strings.Contains(res.YAML, "MATCH,REJECT") {
		t.Fatalf("空配置不对：\n%s", res.YAML)
	}
}
