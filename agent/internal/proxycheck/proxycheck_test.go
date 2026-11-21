package proxycheck

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 一个最小的 HTTP 代理：检查认证，然后把请求转给真正的目标。
func fakeProxy(t *testing.T, user, pass string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := parseBasic(r.Header.Get("Proxy-Authorization")); !ok || u != user || p != pass {
			w.Header().Set("Proxy-Authenticate", `Basic realm="x"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		resp, err := http.Get(r.URL.String())
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
}

func parseBasic(h string) (string, string, bool) {
	r := http.Request{Header: http.Header{"Authorization": {h}}}
	return r.BasicAuth()
}

func TestCheckThroughHTTPProxy(t *testing.T) {
	geoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"success":true,"ip":"72.14.1.21","country_code":"US","region":"Texas","city":"Dallas","timezone":{"id":"America/Chicago"}}`)
	}))
	defer geoSrv.Close()
	GeoURL = geoSrv.URL
	px := fakeProxy(t, "u", "p")
	defer px.Close()
	host := strings.TrimPrefix(px.URL, "http://")

	got := Check(context.Background(), store.Proxy{Protocol: "http", Address: "u:p@" + host})
	if !got.OK || got.ExitIP != "72.14.1.21" || got.Country != "US" || got.Timezone != "America/Chicago" {
		t.Fatalf("结果不对：%+v", got)
	}

	bad := Check(context.Background(), store.Proxy{Protocol: "http", Address: "http://u:wrong@" + host})
	if bad.OK || !strings.Contains(bad.Error, "407") {
		t.Fatalf("密码错应提示 407：%+v", bad)
	}
}

func TestCheckExplainsFailures(t *testing.T) {
	// 端口 1 没人听：拒绝连接。（以前是先占一个空闲端口再关掉，关掉后可能被同时跑的别的测试占用，
	// 2026-11-15 实际碰到一次：那边正好是个 SOCKS5，报成了「认证失败」。）
	addr := "127.0.0.1:1"
	if got := Check(context.Background(), store.Proxy{Protocol: "socks5", Address: "socks5://" + addr}); got.OK || !strings.Contains(got.Error, "拒绝连接") {
		t.Fatalf("应提示拒绝连接：%+v", got)
	}
	if got := Check(context.Background(), store.Proxy{Protocol: "vless", Address: "vless://x"}); got.OK || !strings.Contains(got.Error, "网络模块") {
		t.Fatalf("vless 应提示等网络模块：%+v", got)
	}
	if got := Check(context.Background(), store.Proxy{Protocol: "http", Address: "http://"}); got.OK || !strings.Contains(got.Error, "格式") {
		t.Fatalf("空地址应提示格式：%+v", got)
	}
}

// 慢节点偶发断开：网络层失败自动再试一次（2026-11-21：能用的家宽节点被一次抖动判成不通）。
func TestCheckRetriesOnce(t *testing.T) {
	geoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"success":true,"ip":"1.2.3.4","country_code":"MM","timezone":{"id":"Asia/Yangon"}}`)
	}))
	defer geoSrv.Close()
	GeoURL = geoSrv.URL
	oldDelay := RetryDelay
	RetryDelay = time.Millisecond
	defer func() { RetryDelay = oldDelay }()
	var n int32
	px := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 { // 第一次直接掐断连接
			c, _, _ := w.(http.Hijacker).Hijack()
			c.Close()
			return
		}
		resp, err := http.Get(r.URL.String())
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(w, resp.Body)
	}))
	defer px.Close()
	got := Check(context.Background(), store.Proxy{Protocol: "http", Address: strings.TrimPrefix(px.URL, "http://")})
	if !got.OK || got.Country != "MM" || atomic.LoadInt32(&n) != 2 {
		t.Fatalf("应重试一次后通过：%+v 次数=%d", got, n)
	}
}
