package network

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

func TestSyncWritesOnlyOnChangeAndReloads(t *testing.T) {
	reloads := 0
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/configs" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotPath = string(b)
		reloads++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	dir := t.TempDir()
	s := &Syncer{Path: filepath.Join(dir, "mihomo", "config.yaml"), Opt: Options{TProxyPort: 7893, DNSListen: "10.88.0.1:1053", APIListen: strings.TrimPrefix(srv.URL, "http://")}}
	st := state()
	ctx := context.Background()
	if err := s.Sync(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(ctx, st); err != nil {
		t.Fatal(err)
	}
	if reloads != 1 {
		t.Fatalf("配置没变不该重复加载，加载了 %d 次", reloads)
	}
	if !strings.Contains(gotPath, "config.yaml") {
		t.Fatalf("加载路径不对：%s", gotPath)
	}
	info, err := os.Stat(s.Path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("配置文件权限应为 600（里面有代理密码）：%v %v", info.Mode(), err)
	}
	st.Phones[0].ProxyID = "p2"
	if err := s.Sync(ctx, st); err != nil || reloads != 2 {
		t.Fatalf("配置变了应重新加载：%v，次数 %d", err, reloads)
	}
	if _, ok := s.Blocked()["d"]; !ok {
		t.Fatal("没代理的设备应记在 Blocked 里")
	}
}

func TestSyncReportsMihomoDown(t *testing.T) {
	s := &Syncer{Path: filepath.Join(t.TempDir(), "c.yaml"), Opt: Options{TProxyPort: 7893, DNSListen: "x", APIListen: "127.0.0.1:1"}}
	if err := s.Sync(context.Background(), store.State{}); err == nil || !strings.Contains(err.Error(), "没在运行") {
		t.Fatalf("mihomo 没开应报错：%v", err)
	}
	if s.LastError() == nil {
		t.Fatal("应记住错误，下次重试")
	}
}
