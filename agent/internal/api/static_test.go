package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticGzipAndCache(t *testing.T) {
	js := strings.Repeat("console.log('hello');", 2000)
	h := newStaticHandler(fstest.MapFS{
		"index.html":      {Data: []byte("<html>hi</html>")},
		"assets/app-1.js": {Data: []byte(js)},
		"assets/logo.png": {Data: []byte("PNG")},
	})
	get := func(p, ae string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		if ae != "" {
			r.Header.Set("Accept-Encoding", ae)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// 支持 gzip：压过、解开和原文一样、可长期缓存
	w := get("/assets/app-1.js", "gzip, deflate, br")
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("gzip 版：%d %v", w.Code, w.Header())
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("Content-Type：%q", w.Header().Get("Content-Type"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != js || w.Body.Len() >= len(js)/5 {
		t.Fatalf("解压不对或没压小：%d → %d", len(js), w.Body.Len())
	}
	// 不支持 / 明确不要：原样
	for _, ae := range []string{"", "gzip;q=0", "br"} {
		if w := get("/assets/app-1.js", ae); w.Header().Get("Content-Encoding") != "" || w.Body.String() != js {
			t.Fatalf("Accept-Encoding=%q 应原样", ae)
		}
	}
	// 图片不压；首页不长期缓存；不存在的 404
	if w := get("/assets/logo.png", "gzip"); w.Header().Get("Content-Encoding") != "" || w.Body.String() != "PNG" {
		t.Fatal("图片不该压")
	}
	if w := get("/", "gzip"); w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" || !strings.Contains(w.Body.String(), "hi") {
		t.Fatalf("首页：%d %v", w.Code, w.Header())
	}
	if w := get("/assets/nope.js", "gzip"); w.Code != 404 {
		t.Fatalf("不存在应 404：%d", w.Code)
	}
}
