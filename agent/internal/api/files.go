package api

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

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
