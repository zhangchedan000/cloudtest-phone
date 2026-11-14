// Package proxycheck 检测代理：通不通、出口 IP 在哪、时区、延迟。
// 管家程序自己直接连（HTTP / HTTPS / SOCKS5）；其他协议（vless、订阅……）要等 mihomo 接进来。
package proxycheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// GeoURL 查出口 IP 的服务（返回 ipwho.is 格式）。测试时换成本地假服务。
var GeoURL = "https://ipwho.is/"

type geo struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	IP          string `json:"ip"`
	CountryCode string `json:"country_code"`
	Region      string `json:"region"`
	City        string `json:"city"`
	Timezone    struct {
		ID string `json:"id"`
	} `json:"timezone"`
}

// Supported 管家程序能直接检测的协议。
func Supported(protocol string) bool {
	switch protocol {
	case "http", "https", "socks5":
		return true
	}
	return false
}

// proxyURL 代理库里的地址可能带不带协议头，统一成 URL。
func proxyURL(px store.Proxy) (*url.URL, error) {
	addr := strings.TrimSpace(px.Address)
	if !strings.Contains(addr, "://") {
		addr = px.Protocol + "://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return nil, errors.New("代理地址格式不对，应为 协议://用户名:密码@地址:端口")
	}
	if u.Scheme == "socks5h" {
		u.Scheme = "socks5"
	}
	return u, nil
}

// Check 经代理访问查 IP 的服务。失败也返回结果（OK=false 和原因），只有调用方式错了才返回 error。
func Check(ctx context.Context, px store.Proxy) store.ProxyCheck {
	res := store.ProxyCheck{CheckedAt: time.Now()}
	if !Supported(px.Protocol) {
		res.Error = fmt.Sprintf("%s 协议要等网络模块（mihomo）接入后才能检测", px.Protocol)
		return res
	}
	u, err := proxyURL(px)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	client := &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(u), TLSHandshakeTimeout: 10 * time.Second, DisableKeepAlives: true},
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, GeoURL, nil)
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		res.Error = plain(err)
		return res
	}
	defer resp.Body.Close()
	res.LatencyMs = int(time.Since(start).Milliseconds())
	if resp.StatusCode == http.StatusProxyAuthRequired {
		res.Error = "代理要求认证，用户名或密码不对（407）"
		return res
	}
	var g geo
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&g); err != nil || !g.Success {
		res.Error = "连上了代理，但查不到出口 IP"
		if g.Message != "" {
			res.Error += "：" + g.Message
		}
		return res
	}
	res.OK = true
	res.ExitIP, res.Country, res.Region, res.City, res.Timezone = g.IP, g.CountryCode, g.Region, g.City, g.Timezone.ID
	return res
}

// plain 把网络错误翻译成大白话。
func plain(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "407") || strings.Contains(msg, "username/password authentication failed"):
		return "代理认证失败，用户名或密码不对"
	case strings.Contains(msg, "connection refused"):
		return "代理拒绝连接（地址或端口不对，或者代理没开）"
	case strings.Contains(msg, "no such host"):
		return "找不到代理地址（域名写错了）"
	case strings.Contains(msg, "Client.Timeout") || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "i/o timeout"):
		return "连接超时（代理太慢或连不上）"
	case strings.Contains(msg, "connection reset") || strings.Contains(msg, "EOF"):
		return "连接被断开（协议选错了，或者代理拒绝了这台机器）"
	}
	return msg
}
