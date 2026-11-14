package api

import (
	"net/http"
	"strconv"

	"github.com/zhangchedan000/prism-phone/agent/internal/phones"
)

// flushWriter 每写一块就推给浏览器（视频流不能攒着）。
type flushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}
	return n, err
}

// stream 实时画面：H.264 裸流（Annex-B），浏览器用 WebCodecs 解码。
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	num := func(k string) int { n, _ := strconv.Atoi(q.Get(k)); return n }
	opt := phones.StreamOptions{Width: num("w"), Height: num("h"), BitRate: num("br")}
	f, _ := w.(http.Flusher)
	err := s.svc.Stream(r.Context(), r.PathValue("id"), opt, flushWriter{w, f}, func() {
		w.Header().Set("Content-Type", "video/h264")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
	})
	if err != nil && w.Header().Get("Content-Type") != "video/h264" {
		fail(w, err)
	}
}
