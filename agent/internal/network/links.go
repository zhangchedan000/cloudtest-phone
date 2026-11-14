package network

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// 分享链接（ss:// vmess:// vless:// trojan:// hysteria2://）→ mihomo 的代理写法。
// 只认常见参数；认不出的链接报错，不猜。

func b64(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), nil
		}
	}
	return "", fmt.Errorf("base64 解不开")
}

func portOK(p string) error {
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("端口不对")
	}
	return nil
}

// linkOutbound 解析分享链接；name 是 mihomo 里的代理名。
func linkOutbound(name, link string) (map[string]any, error) {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(link), "://")
	if !ok {
		return nil, fmt.Errorf("不是链接")
	}
	switch strings.ToLower(scheme) {
	case "vmess":
		return vmess(name, rest)
	case "ss":
		return shadowsocks(name, rest)
	case "vless", "trojan", "hysteria2", "hy2":
		return urlStyle(name, strings.ToLower(scheme), link)
	}
	return nil, fmt.Errorf("%s 协议还没接入", scheme)
}

// vmess://base64(JSON)，v2rayN 格式。
func vmess(name, rest string) (map[string]any, error) {
	raw, err := b64(strings.SplitN(rest, "#", 2)[0])
	if err != nil {
		return nil, fmt.Errorf("vmess 链接%v", err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("vmess 链接内容不是 JSON")
	}
	str := func(k string) string { return strings.TrimSpace(fmt.Sprint(v[k])) }
	server, port, id := str("add"), str("port"), str("id")
	if server == "" || id == "" || portOK(port) != nil {
		return nil, fmt.Errorf("vmess 链接缺地址、端口或 id")
	}
	out := map[string]any{"name": name, "type": "vmess", "server": server, "port": port, "uuid": id, "cipher": "auto", "udp": true}
	if aid := str("aid"); aid != "" && aid != "<nil>" {
		out["alterId"] = aid
	} else {
		out["alterId"] = "0"
	}
	if str("tls") == "tls" {
		out["tls"] = true
		if sni := str("sni"); sni != "" && sni != "<nil>" {
			out["servername"] = sni
		}
	}
	switch str("net") {
	case "ws":
		out["network"] = "ws"
		opts := map[string]any{}
		if p := str("path"); p != "" && p != "<nil>" {
			opts["path"] = p
		}
		if h := str("host"); h != "" && h != "<nil>" {
			opts["headers"] = map[string]any{"Host": h}
		}
		out["ws-opts"] = opts
	case "grpc":
		out["network"] = "grpc"
		out["grpc-opts"] = map[string]any{"grpc-service-name": str("path")}
	}
	return out, nil
}

// ss://base64(method:password)@host:port 或 ss://base64(method:password@host:port)。
func shadowsocks(name, rest string) (map[string]any, error) {
	rest = strings.SplitN(rest, "#", 2)[0]
	rest = strings.SplitN(rest, "?", 2)[0]
	userinfo, hostport, ok := strings.Cut(rest, "@")
	if !ok {
		decoded, err := b64(rest)
		if err != nil {
			return nil, fmt.Errorf("ss 链接%v", err)
		}
		userinfo, hostport, ok = strings.Cut(decoded, "@")
		if !ok {
			return nil, fmt.Errorf("ss 链接缺地址")
		}
	} else if dec, err := b64(userinfo); err == nil && strings.Contains(dec, ":") {
		userinfo = dec
	} else if u, err := url.PathUnescape(userinfo); err == nil {
		userinfo = u
	}
	method, password, ok := strings.Cut(userinfo, ":")
	if !ok {
		return nil, fmt.Errorf("ss 链接缺加密方式或密码")
	}
	idx := strings.LastIndex(hostport, ":")
	if idx <= 0 || portOK(hostport[idx+1:]) != nil {
		return nil, fmt.Errorf("ss 链接地址或端口不对")
	}
	return map[string]any{"name": name, "type": "ss", "server": strings.Trim(hostport[:idx], "[]"), "port": hostport[idx+1:], "cipher": method, "password": password, "udp": true}, nil
}

// vless / trojan / hysteria2：标准 URL 形式。
func urlStyle(name, scheme, link string) (map[string]any, error) {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" || portOK(u.Port()) != nil || u.User == nil {
		return nil, fmt.Errorf("%s 链接缺地址、端口或密码", scheme)
	}
	q := u.Query()
	cred := u.User.Username()
	if p, ok := u.User.Password(); ok && scheme != "vless" {
		cred = cred + ":" + p // hysteria2 的 user:pass 形式
	}
	out := map[string]any{"name": name, "server": u.Hostname(), "port": u.Port(), "udp": true}
	sni := q.Get("sni")
	if sni == "" {
		sni = q.Get("peer")
	}
	switch scheme {
	case "vless":
		out["type"] = "vless"
		out["uuid"] = cred
		if f := q.Get("flow"); f != "" {
			out["flow"] = f
		}
		switch q.Get("security") {
		case "tls":
			out["tls"] = true
		case "reality":
			out["tls"] = true
			out["reality-opts"] = map[string]any{"public-key": q.Get("pbk"), "short-id": q.Get("sid")}
			if fp := q.Get("fp"); fp != "" {
				out["client-fingerprint"] = fp
			}
		}
		if sni != "" {
			out["servername"] = sni
		}
	case "trojan":
		out["type"] = "trojan"
		out["password"] = cred
		if sni != "" {
			out["sni"] = sni
		}
	default:
		out["type"] = "hysteria2"
		out["password"] = cred
		if sni != "" {
			out["sni"] = sni
		}
		if q.Get("insecure") == "1" {
			out["skip-cert-verify"] = true
		}
	}
	switch q.Get("type") {
	case "ws":
		out["network"] = "ws"
		opts := map[string]any{}
		if p := q.Get("path"); p != "" {
			opts["path"] = p
		}
		if h := q.Get("host"); h != "" {
			opts["headers"] = map[string]any{"Host": h}
		}
		out["ws-opts"] = opts
	case "grpc":
		out["network"] = "grpc"
		out["grpc-opts"] = map[string]any{"grpc-service-name": q.Get("serviceName")}
	}
	return out, nil
}

func urlUnescape(s string) (string, error) { return url.PathUnescape(s) }
