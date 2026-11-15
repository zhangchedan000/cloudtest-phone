package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 假的「另一台服务器」：口令 t2 才让看 /api/host。
func fakeRemote(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/host" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer t2" {
			http.Error(w, `{"error":"需要登录"}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":"rk3588-01","name":"板子 1"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestServers(t *testing.T) {
	srv, st := newTestServer(t, "")
	remote := fakeRemote(t)
	var sv store.Server
	if code := do(t, srv, "POST", "/servers", `{"url":"`+remote.URL+`/some/path/","token":"t2"}`, &sv); code != 201 {
		t.Fatalf("添加服务器：%d", code)
	}
	if sv.Name != "板子 1" || sv.URL != remote.URL || sv.Token != "t2" || sv.ID == "" {
		t.Fatalf("没填名称应取那台的机器名，地址去掉路径：%+v", sv)
	}
	if code := do(t, srv, "POST", "/servers", `{"url":"`+remote.URL+`","token":"t2"}`, nil); code != 400 {
		t.Fatalf("重复添加应拒绝：%d", code)
	}
	var e struct{ Error string }
	if code := do(t, srv, "POST", "/servers", `{"url":"`+remote.URL+`/x","token":"wrong"}`, &e); code != 400 || !strings.Contains(e.Error, "口令不对") {
		t.Fatalf("口令错应提示：%d %s", code, e.Error)
	}
	if code := do(t, srv, "POST", "/servers", `{"url":"ftp://a","token":"t2"}`, nil); code != 400 {
		t.Fatalf("地址不对应拒绝：%d", code)
	}
	if code := do(t, srv, "PUT", "/servers/"+sv.ID, `{"name":"美国板子"}`, &sv); code != 200 || sv.Name != "美国板子" || sv.Token != "t2" {
		t.Fatalf("改名：%d %+v", code, sv)
	}
	var list []store.Server
	do(t, srv, "GET", "/servers", "", &list)
	if len(list) != 1 {
		t.Fatalf("列表：%+v", list)
	}
	if code := do(t, srv, "DELETE", "/servers/"+sv.ID, "", nil); code != 200 || len(st.Snapshot().Servers) != 0 {
		t.Fatalf("删除：%d", code)
	}
}

// 设了口令才允许跨域；预检请求不用口令。
func TestCORS(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/phones", nil)
	req.Header.Set("Origin", "https://phone.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "https://phone.example.com" {
		t.Fatalf("预检：%d %v", resp.StatusCode, resp.Header)
	}
	// 跨域但没口令：照样 401
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/proxies", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("没口令应 401：%d", resp.StatusCode)
	}
	// 没设口令的服务器不开跨域
	open, _ := newTestServer(t, "")
	req, _ = http.NewRequest(http.MethodOptions, open.URL+"/api/phones", nil)
	req.Header.Set("Origin", "https://phone.example.com")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("没口令的服务器不该允许跨域")
	}
}
