// Package api 是管家程序的 HTTP 接口，同时提供管理页网页。
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	netcfg "github.com/zhangchedan000/prism-phone/agent/internal/network"
	"github.com/zhangchedan000/prism-phone/agent/internal/phones"
	"github.com/zhangchedan000/prism-phone/agent/internal/proxycheck"
	"github.com/zhangchedan000/prism-phone/agent/internal/selfcheck"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

type Server struct {
	svc   *phones.Service
	store *store.Store
	web   fs.FS
	token string
	check func() selfcheck.Report
	// netCheck 网络模块开着时，经 mihomo 检测 HTTP / SOCKS5 以外的协议（vless、trojan、Clash 节点……）。
	netCheck func(ctx context.Context, st store.State, px store.Proxy) store.ProxyCheck
	// netHealth 网络模块的宿主机健康检查结果（策略路由、mihomo），管理页顶部告警用。
	netHealth func() any

	checkMu  sync.Mutex // cachedCheck 的缓存
	checkRep selfcheck.Report
	checkAt  time.Time
}

// SetNetworkHealth 接上网络模块的健康检查。
func (s *Server) SetNetworkHealth(fn func() any) { s.netHealth = fn }

// SetNetworkCheck 接上网络模块的节点检测。
func (s *Server) SetNetworkCheck(fn func(ctx context.Context, st store.State, px store.Proxy) store.ProxyCheck) {
	s.netCheck = fn
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
	mux.HandleFunc("POST /api/models/reload", s.reloadModels)
	mux.HandleFunc("POST /api/models/import", s.importModels)
	mux.HandleFunc("GET /api/phones", s.listPhones)
	mux.HandleFunc("POST /api/phones", s.createPhone)
	mux.HandleFunc("PUT /api/phones/{id}", s.savePhone)
	mux.HandleFunc("DELETE /api/phones/{id}", s.notDuringDataJob(s.deletePhone))
	mux.HandleFunc("POST /api/phones/{id}/start", s.notDuringDataJob(s.action(func(r *http.Request, id string) error { return s.svc.Start(r.Context(), id) })))
	mux.HandleFunc("POST /api/phones/{id}/stop", s.notDuringDataJob(s.action(func(r *http.Request, id string) error { return s.svc.Stop(r.Context(), id) })))
	mux.HandleFunc("POST /api/phones/{id}/reset", s.notDuringDataJob(s.resetPhone))
	mux.HandleFunc("POST /api/phones/{id}/renew", s.action(func(_ *http.Request, id string) error { return s.svc.Renew(id) }))
	mux.HandleFunc("GET /api/phones/{id}/screen", s.screen)
	mux.HandleFunc("GET /api/phones/{id}/stream", s.stream)
	mux.HandleFunc("POST /api/phones/{id}/input", s.input)
	mux.HandleFunc("POST /api/phones/{id}/proxy", s.switchProxy)
	mux.HandleFunc("POST /api/phones/{id}/import-params", s.importParams)
	mux.HandleFunc("GET /api/phones/{id}/clipboard", s.getClipboard)
	mux.HandleFunc("POST /api/phones/{id}/clipboard", s.setClipboard)
	mux.HandleFunc("POST /api/phones/{id}/adb-key", s.authorizeADB)
	mux.HandleFunc("POST /api/phones/{id}/apk", s.installAPK)
	mux.HandleFunc("GET /api/phones/{id}/logs", s.logs)
	mux.HandleFunc("POST /api/phones/{id}/files", s.uploadFile)
	mux.HandleFunc("POST /api/batch/apk", s.batchAPK)
	mux.HandleFunc("POST /api/batch/files", s.batchFiles)
	mux.HandleFunc("GET /api/proxies", s.listProxies)
	mux.HandleFunc("POST /api/proxies", s.createProxy)
	mux.HandleFunc("PUT /api/proxies/{id}", s.updateProxy)
	mux.HandleFunc("DELETE /api/proxies/{id}", s.deleteProxy)
	mux.HandleFunc("POST /api/proxies/{id}/check", s.checkProxy)
	mux.HandleFunc("POST /api/proxies/import", s.importProxies)
	mux.HandleFunc("GET /api/backups", s.listBackups)
	mux.HandleFunc("POST /api/phones/{id}/backup", s.createBackup)
	mux.HandleFunc("POST /api/phones/{id}/restore", s.restoreBackup)
	mux.HandleFunc("DELETE /api/backups/{bid}", s.deleteBackup)
	mux.HandleFunc("GET /api/trash", s.listTrash)
	mux.HandleFunc("DELETE /api/trash/{name}", s.deleteTrash)
	mux.HandleFunc("GET /api/servers", s.listServers)
	mux.HandleFunc("POST /api/servers", s.addServer)
	mux.HandleFunc("PUT /api/servers/{id}", s.updateServer)
	mux.HandleFunc("DELETE /api/servers/{id}", s.deleteServer)
	mux.HandleFunc("GET /api/oled", s.oledState)
	mux.HandleFunc("POST /api/oled", s.oledSet)
	mux.HandleFunc("GET /api/groups", s.listGroups)
	mux.HandleFunc("POST /api/groups", s.createGroup)
	mux.HandleFunc("PUT /api/groups/{id}", s.updateGroup)
	mux.HandleFunc("DELETE /api/groups/{id}", s.deleteGroup)
	if s.web != nil {
		mux.Handle("/", newStaticHandler(s.web)) // gzip + 缓存（static.go）
	}
	return s.cors(s.auth(mux))
}

// auth：设置了 token 就要求 /api/* 带上（请求头 Authorization: Bearer xxx，或 cookie pp_token）。
// 网页本身不需要 token，打开后在页面里填一次。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !safeAPIPath(r) {
			writeErr(w, http.StatusBadRequest, "路径不合法")
			return
		}
		if s.token == "" || !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.tokenOK(r) {
			writeErr(w, http.StatusUnauthorized, "需要登录（token 不对）")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// tokenOK 请求带的口令对不对（请求头 Authorization: Bearer xxx，或 cookie pp_token）。没设口令时都算对。
func (s *Server) tokenOK(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if c, err := r.Cookie("pp_token"); got == "" && err == nil {
		got = c.Value
		// 管理页写 cookie 时用了 encodeURIComponent，这里还原
		if v, err := url.QueryUnescape(got); err == nil {
			got = v
		}
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

// cachedCheck 自检结果缓存 5 秒：/api/health 不用登录、谁都能频繁调，每次都真跑一遍（ping Docker、查磁盘）白耗资源。
func (s *Server) cachedCheck() selfcheck.Report {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	if s.checkAt.IsZero() || time.Since(s.checkAt) > 5*time.Second {
		s.checkRep, s.checkAt = s.check(), time.Now()
	}
	return s.checkRep
}

// safeAPIPath 挡住路径里的「..」和编码过的斜杠：{id} 这类参数会被拼进宿主机路径
// （删手机时 DataDir/phones/{id} 会被挪进回收站），绝不能跳出数据目录。
func safeAPIPath(r *http.Request) bool {
	raw := strings.ToLower(r.URL.RawPath)
	if strings.Contains(raw, "%2f") || strings.Contains(raw, "%5c") || strings.Contains(raw, "%00") {
		return false
	}
	for _, seg := range strings.Split(r.URL.Path, "/") {
		if seg == ".." || seg == "." || strings.ContainsAny(seg, "\\\x00") {
			return false
		}
	}
	return true
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
	case errors.Is(err, phones.ErrBlocked), errors.Is(err, phones.ErrNotRunning), errors.Is(err, phones.ErrNotReady), errors.Is(err, ErrInUse):
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

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	rep := s.cachedCheck()
	status := http.StatusOK
	if !rep.OK {
		status = http.StatusServiceUnavailable
	}
	busy := s.svc != nil && s.svc.Busy()
	// 不用登录：只告诉「好没好、忙不忙」（安装 / 自动更新脚本只看这个）；带了正确口令才给完整自检
	// （内核、内存、磁盘这些机器信息不对没登录的人公开）。
	if !s.tokenOK(r) {
		writeJSON(w, status, map[string]bool{"ok": rep.OK, "busy": busy})
		return
	}
	// busy：有手机正在做开机后设置，自动更新看到就先不重启管家（host/auto-update.sh）
	writeJSON(w, status, struct {
		selfcheck.Report
		Busy bool `json:"busy"`
	}{rep, busy})
}

func (s *Server) host(w http.ResponseWriter, _ *http.Request) {
	cfg := s.svc.Config()
	rep := s.cachedCheck()
	out := map[string]any{
		"id": cfg.HostID, "name": cfg.HostName, "gpuFamily": cfg.GPUFamily, "devHost": cfg.DevHost,
		"maxRunning": cfg.MaxRunning, "androidVersions": versions(cfg.Images), "system": rep.System,
		"network": s.svc.NetworkEnabled(), "gappsVersions": versions(cfg.GappsImages),
	}
	// 正面小屏：有 /dev/i2c-3 才算这台机器带屏（魔云腾 Q1 盒子带，服务器一般没有）。
	if _, err := os.Stat("/dev/i2c-3"); err == nil {
		out["oled"] = true
		_, offErr := os.Stat(oledOffFlag)
		out["oledOn"] = os.IsNotExist(offErr)
	}
	if s.netHealth != nil {
		if h := s.netHealth(); h != nil {
			out["netHealth"] = h
		}
	}
	writeJSON(w, http.StatusOK, out)
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

// reloadModels 重新读本地机型库（导入机型后）。
func (s *Server) reloadModels(w http.ResponseWriter, _ *http.Request) {
	builtin, local := s.svc.ReloadModels()
	writeJSON(w, http.StatusOK, map[string]int{"builtin": builtin, "local": local})
}

// importModels 上传机型包（multipart，字段名 file，可以多个）导入本地机型库。
func (s *Server) importModels(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 200<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "上传格式不对：" + err.Error()})
		return
	}
	pkgs := map[string][]byte{}
	for _, fh := range r.MultipartForm.File["file"] {
		f, err := fh.Open()
		if err != nil {
			fail(w, err)
			return
		}
		b, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			fail(w, err)
			return
		}
		pkgs[fh.Filename] = b
	}
	if len(pkgs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "没有收到机型包（字段名 file）"})
		return
	}
	res, err := s.svc.ImportModels(r.Context(), pkgs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "output": res.Output})
		return
	}
	writeJSON(w, http.StatusOK, res)
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

func (s *Server) resetPhone(w http.ResponseWriter, r *http.Request) {
	var req phones.ResetRequest
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			fail(w, err)
			return
		}
	}
	if err := s.svc.Reset(r.Context(), r.PathValue("id"), req); err != nil {
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
	protocolFromAddress(&px)
	px.Check = nil
	if err := s.store.Update(func(st *store.State) error {
		px.ID = st.FreeID("p-" + time.Now().Format("060102150405.000"))
		st.Proxies = append(st.Proxies, px)
		return nil
	}); err != nil {
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
	if err := s.store.Update(func(st *store.State) error {
		g.ID = st.FreeID("g-" + time.Now().Format("060102150405.000"))
		st.Groups = append(st.Groups, g)
		return nil
	}); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

// screen 截图。不带参数 = 原图 PNG；带 ?w=540 = 缩到这个宽度以内的 JPEG（小很多，页面预览用）。
// 都会在响应头 X-Screen-Width / X-Screen-Height 给出手机真实分辨率（页面换算点击坐标要用）。
func (s *Server) screen(w http.ResponseWriter, r *http.Request) {
	png, err := s.svc.Screenshot(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Access-Control-Expose-Headers", "X-Screen-Width, X-Screen-Height")
	if maxW, _ := strconv.Atoi(r.URL.Query().Get("w")); maxW >= 64 {
		if jpg, sw, sh, err := shrinkToJPEG(png, maxW, 75); err == nil {
			w.Header().Set("X-Screen-Width", strconv.Itoa(sw))
			w.Header().Set("X-Screen-Height", strconv.Itoa(sh))
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpg)
			return
		}
		// 解不开就退回原图
	}
	if cfg, err := pngConfig(png); err == nil {
		w.Header().Set("X-Screen-Width", strconv.Itoa(cfg.Width))
		w.Header().Set("X-Screen-Height", strconv.Itoa(cfg.Height))
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

func (s *Server) input(w http.ResponseWriter, r *http.Request) {
	var req phones.InputRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	if req.Kind == "rotate" {
		// 横竖屏切换：告诉页面转完是不是横的（桌面锁竖屏，转不过去要提示）
		want, landscape, err := s.svc.Rotate(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "wantLandscape": want, "landscape": landscape})
		return
	}
	if err := s.svc.Input(r.Context(), r.PathValue("id"), req); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// switchProxy 切换这台手机当前用的代理：POST {"proxyId": "..."}（一台手机可以挂多个代理）。
// importParams 导入真机身份（真机参数）：粘贴一份真机 getprop dump，套到这台手机上。
func (s *Server) importParams(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dump string `json:"dump"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	imported, missing, err := s.svc.ImportDeviceParams(r.PathValue("id"), req.Dump)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "missing": missing})
}

func (s *Server) switchProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProxyID string `json:"proxyId"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	res, err := s.svc.SwitchProxy(r.Context(), r.PathValue("id"), req.ProxyID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// getClipboard 读手机剪贴板（管理页「取回手机剪贴板」）。
func (s *Server) getClipboard(w http.ResponseWriter, r *http.Request) {
	text, err := s.svc.GetClipboard(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// setClipboard 把文字放进手机剪贴板；paste=true 时顺便粘贴到当前输入框。
func (s *Server) setClipboard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text  string `json:"text"`
		Paste bool   `json:"paste"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := s.svc.SetClipboard(r.Context(), r.PathValue("id"), req.Text, req.Paste); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// authorizeADB 把用户电脑的 ADB 公钥（adbkey.pub）加进手机，之后 adb connect 不再 unauthorized。
func (s *Server) authorizeADB(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := s.svc.AuthorizeADBKey(r.Context(), r.PathValue("id"), req.Key); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ErrInUse 代理还被设备用着，不能删。
var ErrInUse = errors.New("还在使用中")

func (s *Server) updateProxy(w http.ResponseWriter, r *http.Request) {
	var in store.Proxy
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Address) == "" {
		writeErr(w, http.StatusBadRequest, "名称和地址不能为空")
		return
	}
	protocolFromAddress(&in)
	id := r.PathValue("id")
	var out store.Proxy
	err := s.store.Update(func(st *store.State) error {
		for i := range st.Proxies {
			if st.Proxies[i].ID == id {
				changed := st.Proxies[i].Address != in.Address || st.Proxies[i].Protocol != in.Protocol
				st.Proxies[i].Name, st.Proxies[i].Protocol, st.Proxies[i].Address, st.Proxies[i].Note = in.Name, in.Protocol, in.Address, in.Note
				if changed {
					st.Proxies[i].Check = nil // 地址变了，上次检测结果作废
				}
				out = st.Proxies[i]
				return nil
			}
		}
		return store.ErrNotFound
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.Update(func(st *store.State) error {
		for _, p := range st.Phones {
			if p.ProxyID == id {
				return fmt.Errorf("%w：设备 %s 还在用这个代理，先给它换一个", ErrInUse, p.Name)
			}
		}
		// 只是挂在某台手机的备用代理列表里（没在用）：从列表里去掉就行
		for i := range st.Phones {
			var keep []string
			for _, pid := range st.Phones[i].ProxyIDs {
				if pid != id {
					keep = append(keep, pid)
				}
			}
			st.Phones[i].ProxyIDs = keep
		}
		for i := range st.Proxies {
			if st.Proxies[i].ID == id {
				st.Proxies = append(st.Proxies[:i], st.Proxies[i+1:]...)
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

// checkProxy 检测代理并把结果存起来（开机前检查会用到）。
func (s *Server) checkProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := s.RunProxyCheck(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// RunProxyCheck 检测一个代理并把结果存起来（管理页「检测」按钮和开机前自动检测共用）。
func (s *Server) RunProxyCheck(ctx context.Context, id string) (store.ProxyCheck, error) {
	st := s.store.Snapshot()
	px := st.Proxy(id)
	if px == nil {
		return store.ProxyCheck{}, store.ErrNotFound
	}
	var res store.ProxyCheck
	if !proxycheck.Supported(px.Protocol) && s.netCheck != nil {
		res = s.netCheck(ctx, st, *px)
	} else {
		res = proxycheck.Check(ctx, *px)
	}
	err := s.store.Update(func(st *store.State) error {
		for i := range st.Proxies {
			if st.Proxies[i].ID == id {
				st.Proxies[i].Check = &res
				return nil
			}
		}
		return store.ErrNotFound
	})
	return res, err
}

// protocolFromAddress 地址带了协议头（socks5:// http:// vless:// …）就以它为准，免得选的协议和地址对不上。
func protocolFromAddress(px *store.Proxy) {
	if i := strings.Index(px.Address, "://"); i > 0 {
		scheme := strings.ToLower(px.Address[:i])
		switch scheme {
		case "socks5h", "socks":
			scheme = "socks5"
		case "hy2":
			scheme = "hysteria2"
		case "http", "https":
			// 订阅链接也是 http(s)://，用户选了订阅就保持订阅
			if px.Protocol == "subscription" {
				return
			}
		}
		px.Protocol = scheme
	}
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	out, err := s.svc.Logs(r.Context(), r.PathValue("id"), lines, r.URL.Query().Get("level"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

// importProxies 导入订阅：给订阅地址（服务器去下载）或直接粘贴订阅内容 / 多行分享链接。
func (s *Server) importProxies(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Text string `json:"text"`
		Note string `json:"note"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	body := req.Text
	note := strings.TrimSpace(req.Note)
	if strings.TrimSpace(req.URL) != "" {
		var err error
		if body, err = netcfg.FetchSubscription(r.Context(), strings.TrimSpace(req.URL)); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if note == "" {
			note = "订阅导入"
		}
	}
	// 自动跟进 proxy-providers：有的机场订阅顶层 proxies 是空的，节点挂在二级 provider 地址上。
	nodes, skipped, err := netcfg.ParseSubscriptionWithProviders(r.Context(), body, netcfg.FetchSubscription)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "skipped": skipped})
		return
	}
	stamp := time.Now().Format("060102150405")
	var added []store.Proxy
	err = s.store.Update(func(st *store.State) error {
		added = netcfg.ImportNodes(st, nodes, note, func(i int) string { return fmt.Sprintf("p-%s-%03d", stamp, i) })
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": len(added), "duplicates": len(nodes) - len(added), "skipped": skipped})
}

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	var in store.Group
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "分组名不能为空")
		return
	}
	id := r.PathValue("id")
	var out store.Group
	err := s.store.Update(func(st *store.State) error {
		for i := range st.Groups {
			if st.Groups[i].ID == id {
				st.Groups[i].Name = strings.TrimSpace(in.Name)
				if in.Color != "" {
					st.Groups[i].Color = in.Color
				}
				out = st.Groups[i]
				return nil
			}
		}
		return store.ErrNotFound
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteGroup 删分组：组里的设备变成「未分组」，设备本身不动。
func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	moved := 0
	err := s.store.Update(func(st *store.State) error {
		found := false
		for i := range st.Groups {
			if st.Groups[i].ID == id {
				st.Groups = append(st.Groups[:i], st.Groups[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return store.ErrNotFound
		}
		for i := range st.Phones {
			if st.Phones[i].GroupID == id {
				st.Phones[i].GroupID = ""
				moved++
			}
		}
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ungrouped": moved})
}

func (s *Server) listTrash(w http.ResponseWriter, _ *http.Request) {
	items, err := s.svc.ListTrash()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) deleteTrash(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteTrash(r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// 备份和恢复（phones/backup.go）。
func (s *Server) listBackups(w http.ResponseWriter, _ *http.Request) {
	items, err := s.svc.ListBackups()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note string `json:"note"`
	}
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			fail(w, err)
			return
		}
	}
	if err := s.svc.StartBackup(r.PathValue("id"), req.Note); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BackupID string `json:"backupId"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := s.svc.StartRestore(r.PathValue("id"), req.BackupID); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteBackup(r.PathValue("bid")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// notDuringDataJob 正在备份 / 恢复的手机不许开关机、重置、删除（会把正在拷的数据弄坏）。
func (s *Server) notDuringDataJob(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.svc.JobRunning(r.PathValue("id")) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "这台手机正在备份或恢复，等它做完再操作"})
			return
		}
		h(w, r)
	}
}
