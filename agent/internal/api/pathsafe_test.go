package api

import (
	"net/http"
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
