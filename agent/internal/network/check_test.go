package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/proxycheck"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

func TestCheckThroughMihomo(t *testing.T) {
	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"success":true,"ip":"9.9.9.9","country_code":"US","city":"Dallas","timezone":{"id":"America/Chicago"}}`)
	}))
	defer geo.Close()
	proxycheck.GeoURL = geo.URL

	var mu sync.Mutex
	var selected []string
	ctrl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/proxies/"+CheckGroup {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			selected = append(selected, string(b))
			mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ctrl.Close()
	// 假的检测入口：普通 HTTP 代理，转发到目标
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	entry := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, err := http.Get(r.URL.String())
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(w, resp.Body)
	})}
	go func() { _ = entry.Serve(ln) }()
	defer entry.Close()
	port, _ := strconv.Atoi(strings.Split(ln.Addr().String(), ":")[1])

	s := &Syncer{Path: filepath.Join(t.TempDir(), "c.yaml"), Opt: Options{TProxyPort: 7893, DNSListen: "x", APIListen: strings.TrimPrefix(ctrl.URL, "http://"), CheckPort: port}}
	px := store.Proxy{ID: "v1", Protocol: "trojan", Address: "trojan://pw@tj.example.com:443"}
	st := store.State{Proxies: []store.Proxy{px}}
	got := s.Check(context.Background(), st, px)
	if !got.OK || got.ExitIP != "9.9.9.9" {
		t.Fatalf("检测结果不对：%+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(selected) != 2 || !strings.Contains(selected[0], "px-v1") || !strings.Contains(selected[1], "REJECT") {
		t.Fatalf("应先切到节点、测完切回 REJECT：%v", selected)
	}
	y := Build(st, s.Opt).YAML
	if !strings.Contains(y, `"name":"pp-check"`) || !strings.Contains(y, `listen: 127.0.0.1, port: `+strconv.Itoa(port)) || !strings.Contains(y, `"px-v1"`) {
		t.Fatalf("配置里应有检测入口：\n%s", y)
	}
	if strings.Contains(y, ",px-v1\n") {
		t.Fatal("没有设备用的节点不应出现在设备规则里")
	}
}
