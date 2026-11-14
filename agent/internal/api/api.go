// Package api 是管家程序的 HTTP 接口，同时提供管理页网页。
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/phones"
	"github.com/zhangchedan000/prism-phone/agent/internal/selfcheck"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

type Server struct {
	svc   *phones.Service
	store *store.Store
	web   fs.FS
	token string
	check func() selfcheck.Report
}

func New(svc *phones.Service, st *store.Store, web fs.FS, token string, check func() selfcheck.Report) *Server {
	return &Server{svc: svc, store: st, web: web, token: token, check: check}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/host", s.host)
	mux.HandleFunc("GET /api/fields", s.fields)
	mux.HandleFunc("GET /api/models", s.models)
	mux.HandleFunc("GET /api/phones", s.listPhones)
	mux.HandleFunc("POST /api/phones", s.createPhone)
	mux.HandleFunc("PUT /api/phones/{id}", s.savePhone)
	mux.HandleFunc("DELETE /api/phones/{id}", s.deletePhone)
	mux.HandleFunc("POST /api/phones/{id}/start", s.action(func(r *http.Request, id string) error { return s.svc.Start(r.Context(), id) }))
	mux.HandleFunc("POST /api/phones/{id}/stop", s.action(func(r *http.Request, id string) error { return s.svc.Stop(r.Context(), id) }))
	mux.HandleFunc("POST /api/phones/{id}/renew", s.action(func(_ *http.Request, id string) error { return s.svc.Renew(id) }))
	mux.HandleFunc("GET /api/proxies", s.listProxies)
	mux.HandleFunc("POST /api/proxies", s.createProxy)
	mux.HandleFunc("GET /api/groups", s.listGroups)
	mux.HandleFunc("POST /api/groups", s.createGroup)
	if s.web != nil {
		mux.Handle("/", http.FileServerFS(s.web))
	}
	return s.auth(mux)
}

// auth：设置了 token 就要求 /api/* 带上（请求头 Authorization: Bearer xxx，或 cookie pp_token）。
// 网页本身不需要 token，打开后在页面里填一次。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" || !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if c, err := r.Cookie("pp_token"); got == "" && err == nil {
			got = c.Value
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "需要登录（token 不对）")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, phones.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, phones.ErrBlocked):
		writeErr(w, http.StatusConflict, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errors.Join(phones.ErrInvalid, err)
	}
	return nil
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	rep := s.check()
	status := http.StatusOK
	if !rep.OK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, rep)
}

func (s *Server) host(w http.ResponseWriter, _ *http.Request) {
	cfg := s.svc.Config()
	rep := s.check()
	writeJSON(w, http.StatusOK, map[string]any{
		"id": cfg.HostID, "name": cfg.HostName, "gpuFamily": cfg.GPUFamily, "devHost": cfg.DevHost,
		"maxRunning": cfg.MaxRunning, "androidVersions": versions(cfg.Images), "system": rep.System,
	})
}

func versions(images map[int]string) []int {
	out := make([]int, 0, len(images))
	for v := range images {
		out = append(out, v)
	}
	return out
}

func (s *Server) fields(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(s.svc.Catalog().Raw)
}

func (s *Server) models(w http.ResponseWriter, _ *http.Request) {
	raws := make([]json.RawMessage, 0)
	for _, m := range s.svc.Models() {
		raws = append(raws, m.Raw)
	}
	writeJSON(w, http.StatusOK, raws)
}

func (s *Server) listPhones(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.List(r.Context()))
}

func (s *Server) createPhone(w http.ResponseWriter, r *http.Request) {
	var req phones.CreateRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	p, err := s.svc.Create(req)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) savePhone(w http.ResponseWriter, r *http.Request) {
	var req phones.SaveRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := s.svc.Save(r.PathValue("id"), req); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deletePhone(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Delete(r.Context(), r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) action(fn func(*http.Request, string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(r, r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (s *Server) listProxies(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Snapshot().Proxies)
}

func (s *Server) createProxy(w http.ResponseWriter, r *http.Request) {
	var px store.Proxy
	if err := decode(r, &px); err != nil {
		fail(w, err)
		return
	}
	if strings.TrimSpace(px.Name) == "" || strings.TrimSpace(px.Address) == "" {
		writeErr(w, http.StatusBadRequest, "名称和地址不能为空")
		return
	}
	px.ID = "p-" + time.Now().Format("060102150405.000")
	px.Check = nil
	if err := s.store.Update(func(st *store.State) error { st.Proxies = append(st.Proxies, px); return nil }); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, px)
}

func (s *Server) listGroups(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Snapshot().Groups)
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var g store.Group
	if err := decode(r, &g); err != nil {
		fail(w, err)
		return
	}
	if strings.TrimSpace(g.Name) == "" {
		writeErr(w, http.StatusBadRequest, "分组名不能为空")
		return
	}
	g.ID = "g-" + time.Now().Format("060102150405.000")
	if err := s.store.Update(func(st *store.State) error { st.Groups = append(st.Groups, g); return nil }); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}
