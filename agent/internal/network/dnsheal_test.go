package network

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 踩坑 #131：开机时 mihomo 的 DNS 没绑上 → 管家发现后让 mihomo 重读配置，再不行重启服务。
func TestHealthHealsDNS(t *testing.T) {
	reloads := 0
	up := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/configs") {
			reloads++
			up = true // 重读配置后 DNS 绑上了
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer api.Close()
	f := &fakeHost{table: true}
	h := &HealthChecker{
		Run:     f.run,
		Syncer:  &Syncer{Path: "/x/config.yaml", Opt: Options{Redir: true, DNSListen: "10.88.0.1:1053", APIListen: strings.TrimPrefix(api.URL, "http://")}},
		HasAddr: func(string) bool { return true },
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			if !up {
				return nil, errors.New("connection refused")
			}
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		},
	}
	st := h.Check(context.Background())
	if !st.OK || reloads != 1 || st.HealedAt == nil {
		t.Fatalf("应重读配置修好：%+v reloads=%d", st, reloads)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "restart") {
			t.Fatalf("重读配置就好了，不该重启：%v", f.calls)
		}
	}
	// 正常时什么都不做
	if st := h.Check(context.Background()); !st.OK || reloads != 1 {
		t.Fatalf("正常时：%+v reloads=%d", st, reloads)
	}
	// 手机网桥还没起来：只报问题，不重读不重启
	up = false
	h.HasAddr = func(string) bool { return false }
	st = h.Check(context.Background())
	if st.OK || reloads != 1 || !strings.Contains(strings.Join(st.Problems, ";"), "手机网桥") {
		t.Fatalf("网桥没起来时：%+v reloads=%d", st, reloads)
	}
}

// 重读配置也绑不上：重启 mihomo 服务。
func TestHealthRestartsMihomoWhenReloadNotEnough(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer api.Close()
	up := false
	f := &fakeHost{table: true}
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "restart") {
			up = true
		}
		return f.run(ctx, name, args...)
	}
	h := &HealthChecker{
		Run:     run,
		Syncer:  &Syncer{Path: "/x", Opt: Options{Redir: true, DNSListen: "10.88.0.1:1053", APIListen: strings.TrimPrefix(api.URL, "http://")}},
		HasAddr: func(string) bool { return true },
		Dial: func(context.Context, string, string) (net.Conn, error) {
			if !up {
				return nil, errors.New("refused")
			}
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		},
	}
	if st := h.Check(context.Background()); !st.OK || !up {
		t.Fatalf("应重启 mihomo 修好：%+v calls=%v", st, f.calls)
	}
}
