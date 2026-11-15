package api

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/zhangchedan000/prism-phone/agent/internal/phones"
)

// firstFile 从 multipart 请求里取第一个文件（边收边传，不整个读进内存）。
func firstFile(w http.ResponseWriter, r *http.Request) (*multipart.Part, error) {
	r.Body = http.MaxBytesReader(w, r.Body, phones.MaxUpload+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("%w：要用表单（multipart/form-data）上传文件", phones.ErrInvalid)
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil, fmt.Errorf("%w：没有收到文件", phones.ErrInvalid)
		}
		if err != nil {
			return nil, fmt.Errorf("%w：文件没传完整（%v）", phones.ErrInvalid, err)
		}
		if part.FileName() != "" {
			return part, nil
		}
	}
}

func (s *Server) installAPK(w http.ResponseWriter, r *http.Request) {
	part, err := firstFile(w, r)
	if err != nil {
		fail(w, err)
		return
	}
	defer part.Close()
	out, err := s.svc.InstallAPK(r.Context(), r.PathValue("id"), part.FileName(), part)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	part, err := firstFile(w, r)
	if err != nil {
		fail(w, err)
		return
	}
	defer part.Close()
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = "Download"
	}
	remote, err := s.svc.UploadFile(r.Context(), r.PathValue("id"), dir, part.FileName(), part)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": remote})
}

// batchIDs 从 ?ids=a,b,c 取手机 ID。
func batchIDs(r *http.Request) []string {
	var ids []string
	for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// batchAPK 批量装 App：POST /api/batch/apk?ids=a,b,c（文件只传一次）。
func (s *Server) batchAPK(w http.ResponseWriter, r *http.Request) {
	ids := batchIDs(r)
	part, err := firstFile(w, r)
	if err != nil {
		fail(w, err)
		return
	}
	defer part.Close()
	results, err := s.svc.InstallAPKMany(r.Context(), ids, part.FileName(), part)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// batchFiles 批量传文件：POST /api/batch/files?ids=a,b,c&dir=DCIM。
func (s *Server) batchFiles(w http.ResponseWriter, r *http.Request) {
	ids := batchIDs(r)
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = "Download"
	}
	part, err := firstFile(w, r)
	if err != nil {
		fail(w, err)
		return
	}
	defer part.Close()
	results, err := s.svc.UploadFileMany(r.Context(), ids, dir, part.FileName(), part)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
