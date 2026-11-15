package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/phones"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 多台服务器统一管理（2026-11-15 用户要求）：
// 主服务器（打开管理页的这台）记着别的服务器的地址和口令；管理页拿到后直接连每一台（画面、操作不经主服务器中转）。
// 所以每台服务器都要允许跨域访问（cors），并且必须设了口令。

// cors 允许别的地址打开的管理页调用这台的接口。只在设了口令时打开：
// 跨域请求不带 cookie，必须带 Authorization 口令，没口令的人照样什么都做不了。
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if s.token != "" && origin != "" && strings.HasPrefix(r.URL.Path, "/api/") {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Expose-Headers", "X-Screen-Width, X-Screen-Height")
			h.Set("Access-Control-Max-Age", "600")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) listServers(w http.ResponseWriter, _ *http.Request) {
	list := s.store.Snapshot().Servers
	if list == nil {
		list = []store.Server{}
	}
	writeJSON(w, http.StatusOK, list)
}

// normalizeServerURL 只留 scheme://host[:port]，去掉末尾的斜杠和路径。
func normalizeServerURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("%w：地址要像 https://phone2.example.com 这样", phones.ErrInvalid)
	}
	return u.Scheme + "://" + u.Host, nil
}

// probeServer 用口令连一下那台的 /api/host，确认地址和口令都对，顺便拿机器名。
func probeServer(ctx context.Context, base, token string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/host", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w：连不上 %s（%v）", phones.ErrInvalid, base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%w：口令不对", phones.ErrInvalid)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w：%s 返回 %d（地址对吗？）", phones.ErrInvalid, base, resp.StatusCode)
	}
	var info map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("%w：%s 不像云手机服务器", phones.ErrInvalid, base)
	}
	return info, nil
}

func (s *Server) addServer(w http.ResponseWriter, r *http.Request) {
	var in store.Server
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	base, err := normalizeServerURL(in.URL)
	if err != nil {
		fail(w, err)
		return
	}
	if strings.TrimSpace(in.Token) == "" {
		writeErr(w, http.StatusBadRequest, "要填那台服务器的口令（PP_TOKEN）")
		return
	}
	info, err := probeServer(r.Context(), base, strings.TrimSpace(in.Token))
	if err != nil {
		fail(w, err)
		return
	}
	self := ""
	if s.svc != nil {
		self = s.svc.Config().HostID
	}
	if id, _ := info["id"].(string); id != "" && id == self {
		writeErr(w, http.StatusBadRequest, "这就是当前这台服务器，不用添加")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name, _ = info["name"].(string)
	}
	out := store.Server{Name: name, URL: base, Token: strings.TrimSpace(in.Token)}
	err = s.store.Update(func(st *store.State) error {
		for _, sv := range st.Servers {
			if sv.URL == base {
				return fmt.Errorf("%w：%s 已经添加过了", phones.ErrInvalid, base)
			}
		}
		out.ID = st.FreeID("s-" + time.Now().Format("060102150405"))
		st.Servers = append(st.Servers, out)
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) {
	var in store.Server
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	id := r.PathValue("id")
	var out store.Server
	err := s.store.Update(func(st *store.State) error {
		for i := range st.Servers {
			if st.Servers[i].ID != id {
				continue
			}
			if n := strings.TrimSpace(in.Name); n != "" {
				st.Servers[i].Name = n
			}
			if t := strings.TrimSpace(in.Token); t != "" {
				st.Servers[i].Token = t
			}
			out = st.Servers[i]
			return nil
		}
		return store.ErrNotFound
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.Update(func(st *store.State) error {
		for i := range st.Servers {
			if st.Servers[i].ID == id {
				st.Servers = append(st.Servers[:i], st.Servers[i+1:]...)
				return nil
			}
		}
		return store.ErrNotFound
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
