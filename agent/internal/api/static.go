package api

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// staticHandler 管理页静态文件。
//   - assets/ 下是带内容哈希的文件名（每次构建都变），浏览器可以一直缓存；
//   - JS / CSS 浏览器支持就发 gzip（1.4MB 的 JS 压完约 0.44MB），第一次请求时压一次、存在内存里；
//   - index.html 每次都问一下（no-cache），更新管家后页面马上用新版本。
type staticHandler struct {
	fsys  fs.FS
	files http.Handler

	mu sync.Mutex
	gz map[string][]byte // 路径 → 压好的内容（nil = 压不了，走原样）
}

func newStaticHandler(fsys fs.FS) *staticHandler {
	return &staticHandler{fsys: fsys, files: http.FileServerFS(fsys), gz: map[string][]byte{}}
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if !strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "no-cache")
		h.files.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if ext := path.Ext(p); ext == ".js" || ext == ".css" || ext == ".svg" || ext == ".json" {
		w.Header().Set("Vary", "Accept-Encoding")
		if acceptsGzip(r) && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			if b := h.gzipped(p); b != nil {
				w.Header().Set("Content-Type", mime.TypeByExtension(ext))
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Content-Length", strconv.Itoa(len(b)))
				if r.Method == http.MethodGet {
					_, _ = w.Write(b)
				}
				return
			}
		}
	}
	h.files.ServeHTTP(w, r)
}

// gzipped 压好的内容；文件不存在或读不了返回 nil（交给 FileServer 回 404 等）。
func (h *staticHandler) gzipped(p string) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	if b, ok := h.gz[p]; ok {
		return b
	}
	raw, err := fs.ReadFile(h.fsys, p)
	if err != nil {
		return nil // 不缓存：不存在的路径别把内存撑大
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(raw)
	if zw.Close() != nil {
		h.gz[p] = nil
		return nil
	}
	h.gz[p] = buf.Bytes()
	return h.gz[p]
}

// acceptsGzip 浏览器说了能收 gzip（Accept-Encoding 里有 gzip 且不是 q=0）。
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		f := strings.Split(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(f[0]), "gzip") {
			for _, q := range f[1:] {
				if v := strings.ReplaceAll(strings.TrimSpace(q), " ", ""); v == "q=0" || v == "q=0.0" || v == "q=0.00" || v == "q=0.000" {
					return false
				}
			}
			return true
		}
	}
	return false
}
