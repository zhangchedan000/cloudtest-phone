package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// {id} 会拼进宿主机路径（删手机时 DataDir/phones/{id} 会被挪走），编码过的 ../ 必须在进处理函数前挡掉。
func TestRejectsPathTraversal(t *testing.T) {
	srv, _ := newTestServer(t, "")
	for _, p := range []string{"/phones/..%2F..%2F..%2Fetc", "/phones/%2E%2E%2F%2E%2E%2Fetc", "/proxies/..%2fx", "/trash/..%5C..%5Cx"} {
		req, _ := http.NewRequest("DELETE", srv.URL+"/api"+p, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s 应被挡成 400，实际 %d", p, resp.StatusCode)
		}
	}
	// 正常 id 不受影响
	if code := do(t, srv, "GET", "/proxies", "", nil); code != 200 {
		t.Fatalf("正常请求被误挡：%d", code)
	}
}

// /api/health 不用登录，但没登录只给 ok / busy，不公开内核、内存、磁盘等机器信息。
func TestHealthMinimalWithoutToken(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	var anon map[string]any
	if code := doAuth(t, srv, "", "/health", &anon); code != 200 {
		t.Fatalf("没登录也该能查健康：%d", code)
	}
	if _, ok := anon["system"]; ok || len(anon) != 2 {
		t.Fatalf("没登录不该给详情：%v", anon)
	}
	var full map[string]any
	_ = doAuth(t, srv, "secret", "/health", &full)
	if _, ok := full["system"]; !ok {
		t.Fatalf("登录了应给完整自检：%v", full)
	}
}

func doAuth(t *testing.T, srv *httptest.Server, token, path string, out any) int {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/api"+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_ = json.NewDecoder(resp.Body).Decode(out)
	return resp.StatusCode
}
