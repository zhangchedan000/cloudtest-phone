package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/selfcheck"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 只测代理库、分组库这些不需要 Docker 的接口。
func newTestServer(t *testing.T, token string) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(nil, st, nil, token, func() selfcheck.Report { return selfcheck.Report{OK: true} }).Handler())
	t.Cleanup(srv.Close)
	return srv, st
}

func do(t *testing.T, srv *httptest.Server, method, path, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+"/api"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestProxyLibrary(t *testing.T) {
	srv, st := newTestServer(t, "")
	var px store.Proxy
	if code := do(t, srv, "POST", "/proxies", `{"name":"A","protocol":"socks5","address":"http://u:p@1.2.3.4:8080"}`, &px); code != 201 {
		t.Fatalf("创建代理：%d", code)
	}
	if px.Protocol != "http" {
		t.Fatalf("协议应以地址为准：%s", px.Protocol)
	}
	_ = st.Update(func(s *store.State) error {
		s.Phones = append(s.Phones, store.Phone{ID: "ph1", Name: "手机1", ProxyID: px.ID})
		return nil
	})
	var e map[string]string
	if code := do(t, srv, "DELETE", "/proxies/"+px.ID, "", &e); code != 409 || !strings.Contains(e["error"], "手机1") {
		t.Fatalf("有设备在用不能删：%d %v", code, e)
	}
	px.Check = &store.ProxyCheck{OK: true}
	_ = st.Update(func(s *store.State) error { s.Proxies[0].Check = px.Check; return nil })
	var upd store.Proxy
	do(t, srv, "PUT", "/proxies/"+px.ID, `{"name":"B","protocol":"http","address":"http://u:p@5.6.7.8:8080"}`, &upd)
	if upd.Name != "B" || upd.Check != nil {
		t.Fatalf("改地址后检测结果应作废：%+v", upd)
	}
	var imp map[string]any
	do(t, srv, "POST", "/proxies/import", `{"text":"trojan://pw@tj.example.com:443#TJ"}`, &imp)
	if imp["added"].(float64) != 1 {
		t.Fatalf("导入：%v", imp)
	}
}

func TestGroups(t *testing.T) {
	srv, st := newTestServer(t, "")
	var g store.Group
	if code := do(t, srv, "POST", "/groups", `{"name":"测试组 A","color":"#2f6bff"}`, &g); code != 201 {
		t.Fatalf("创建分组：%d", code)
	}
	_ = st.Update(func(s *store.State) error {
		s.Phones = append(s.Phones, store.Phone{ID: "ph1", GroupID: g.ID})
		return nil
	})
	var upd store.Group
	do(t, srv, "PUT", "/groups/"+g.ID, `{"name":"测试组 B"}`, &upd)
	if upd.Name != "测试组 B" || upd.Color != "#2f6bff" {
		t.Fatalf("改名不对：%+v", upd)
	}
	if code := do(t, srv, "PUT", "/groups/"+g.ID, `{"name":"  "}`, nil); code != 400 {
		t.Fatalf("空名字应拒绝：%d", code)
	}
	var del map[string]any
	do(t, srv, "DELETE", "/groups/"+g.ID, "", &del)
	if del["ungrouped"].(float64) != 1 || st.Snapshot().Phones[0].GroupID != "" {
		t.Fatalf("删分组后设备应变未分组：%v %+v", del, st.Snapshot().Phones)
	}
	if code := do(t, srv, "DELETE", "/groups/nope", "", nil); code != 404 {
		t.Fatalf("不存在的分组：%d", code)
	}
}

func TestTokenRequired(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	if code := do(t, srv, "GET", "/proxies", "", nil); code != 401 {
		t.Fatalf("没带口令应 401：%d", code)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/proxies", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("带对口令应 200：%d", resp.StatusCode)
	}
	if code := do(t, srv, "GET", "/health", "", nil); code != 200 {
		t.Fatalf("健康检查不需要口令：%d", code)
	}
}
