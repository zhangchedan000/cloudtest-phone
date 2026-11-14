package network

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

func TestShareLinks(t *testing.T) {
	vm, _ := json.Marshal(map[string]any{"v": "2", "add": "vm.example.com", "port": "443", "id": "u-1", "aid": "0", "net": "ws", "path": "/ws", "host": "cdn.example.com", "tls": "tls", "sni": "vm.example.com"})
	ssInfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pa:ss"))
	cases := []struct {
		link string
		want map[string]any
	}{
		{"vmess://" + base64.StdEncoding.EncodeToString(vm), map[string]any{"type": "vmess", "server": "vm.example.com", "port": 443, "uuid": "u-1", "network": "ws", "tls": true}},
		{"ss://" + ssInfo + "@1.2.3.4:8388#名字", map[string]any{"type": "ss", "server": "1.2.3.4", "port": 8388, "cipher": "aes-256-gcm", "password": "pa:ss"}},
		{"ss://" + base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pw@5.6.7.8:443")), map[string]any{"type": "ss", "server": "5.6.7.8", "cipher": "chacha20-ietf-poly1305", "password": "pw"}},
		{"vless://uuid-2@vl.example.com:443?security=reality&pbk=PK&sid=ab&sni=www.example.com&fp=chrome&flow=xtls-rprx-vision&type=tcp#x", map[string]any{"type": "vless", "uuid": "uuid-2", "servername": "www.example.com", "flow": "xtls-rprx-vision", "tls": true}},
		{"trojan://secret@tj.example.com:443?sni=tj.example.com&type=ws&path=%2Ft", map[string]any{"type": "trojan", "password": "secret", "sni": "tj.example.com", "network": "ws"}},
		{"hy2://pw@hy.example.com:8443?sni=hy.example.com&insecure=1", map[string]any{"type": "hysteria2", "password": "pw", "skip-cert-verify": true}},
	}
	for _, c := range cases {
		got, err := outbound(store.Proxy{ID: "x", Address: c.link})
		if err != nil {
			t.Fatalf("%s：%v", c.link[:12], err)
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Fatalf("%s：%s = %v，想要 %v（%v）", c.link[:12], k, got[k], v, got)
			}
		}
	}
	for _, bad := range []string{"vmess://bm90IGpzb24=", "ss://xx@h", "vless://h:443", "tuic://a@b:1", "socks5://h"} {
		if _, err := outbound(store.Proxy{ID: "x", Address: bad}); err == nil {
			t.Fatalf("%s 应该被拒绝", bad)
		}
	}
}

func TestShareLinkInConfig(t *testing.T) {
	st := store.State{
		Proxies: []store.Proxy{{ID: "p", Protocol: "trojan", Address: "trojan://pw@tj.example.com:443"}},
		Phones:  []store.Phone{{ID: "a", IP: "10.88.0.10", ProxyID: "p"}},
	}
	y := Build(st, Options{TProxyPort: 7893, DNSListen: "x"}).YAML
	if !strings.Contains(y, `"type":"trojan"`) || !strings.Contains(y, "SRC-IP-CIDR,10.88.0.10/32,px-p") {
		t.Fatalf("配置不对：\n%s", y)
	}
}
