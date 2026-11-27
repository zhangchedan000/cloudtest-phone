package api

import (
	"context"
	"net/http"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/selfupdate"
)

// updateStatus GET /api/update：当前版本 + GitHub 上最新版本 + 能不能升级（在线升级功能）。
func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	st := selfupdate.Check(ctx, s.version)
	s.upgradeMu.Lock()
	up := s.upgrading
	s.upgradeMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"current": st.Current, "latest": st.Latest, "canUpdate": st.CanUpdate, "note": st.Note, "upgrading": up,
	})
}

// doUpdate POST /api/update：开始在线升级（下载→SHA 校验→后台脚本换新重启→失败回滚）。
// 很快返回（真正换新在后台独立脚本里做）；前端拿到后轮询 /api/health 等新版本起来。
func (s *Server) doUpdate(w http.ResponseWriter, r *http.Request) {
	s.upgradeMu.Lock()
	if s.upgrading {
		s.upgradeMu.Unlock()
		writeErr(w, http.StatusConflict, "正在升级中，别重复点")
		return
	}
	s.upgrading = true
	s.upgradeMu.Unlock()
	// 升级没真正开始（下载/校验失败）要把标记复位；成功的话进程马上会被重启，无所谓。
	started := false
	defer func() {
		if !started {
			s.upgradeMu.Lock()
			s.upgrading = false
			s.upgradeMu.Unlock()
		}
	}()

	health := selfupdate.HealthURL(s.plainListen)
	newVer, err := selfupdate.Start(r.Context(), s.version, health)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	started = true
	writeJSON(w, http.StatusOK, map[string]any{"from": s.version, "to": newVer, "upgrading": true})
}
