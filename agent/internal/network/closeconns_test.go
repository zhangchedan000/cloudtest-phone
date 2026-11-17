package network

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestCloseConnections(t *testing.T) {
	var mu sync.Mutex
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/connections":
			_ = json.NewEncoder(w).Encode(map[string]any{"connections": []map[string]any{
				{"id": "a", "metadata": map[string]any{"sourceIP": "10.88.1.4"}},
				{"id": "b", "metadata": map[string]any{"sourceIP": "10.88.1.5"}},
				{"id": "c", "metadata": map[string]any{"sourceIP": "10.88.1.4"}},
			}})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/connections/"):
			mu.Lock()
			deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/connections/"))
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := &Syncer{Opt: Options{APIListen: strings.TrimPrefix(srv.URL, "http://")}}
	n, err := s.CloseConnections(context.Background(), "10.88.1.4")
	if err != nil || n != 2 || strings.Join(deleted, ",") != "a,c" {
		t.Fatalf("n=%d err=%v deleted=%v", n, err, deleted)
	}
	if n, _ := (&Syncer{}).CloseConnections(context.Background(), "10.88.1.4"); n != 0 {
		t.Fatal("没开网络模块应该什么都不做")
	}
}
