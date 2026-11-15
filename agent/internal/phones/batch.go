package phones

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
)

// 批量装 App / 批量传文件（2026-11-15）：勾选多台，文件只从电脑上传一次，服务器再分发到每台。
// （以前只能一台一台点；一个 279 MB 的 APK 装 10 台要从国内上传 10 遍。）

// BatchResult 一台手机的结果。
type BatchResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Path  string `json:"path,omitempty"` // 传文件：手机里的路径
}

// MaxBatch 一次最多多少台。
const MaxBatch = 100

func checkBatchIDs(ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w：没有选手机", ErrInvalid)
	}
	if len(ids) > MaxBatch {
		return fmt.Errorf("%w：一次最多 %d 台", ErrInvalid, MaxBatch)
	}
	return nil
}

// eachPhone 最多同时 3 台（docker cp + 安装很吃硬盘和 CPU），结果按传进来的顺序。
func eachPhone(ids []string, fn func(id string) BatchResult) []BatchResult {
	out := make([]BatchResult, len(ids))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = fn(id)
		}(i, id)
	}
	wg.Wait()
	return out
}

// InstallAPKMany 给多台手机装同一个 APK。关机的、开机中的会报错，其他照装。
func (s *Service) InstallAPKMany(ctx context.Context, ids []string, fileName string, r io.Reader) ([]BatchResult, error) {
	if err := checkBatchIDs(ids); err != nil {
		return nil, err
	}
	if !strings.EqualFold(path.Ext(fileName), ".apk") {
		return nil, fmt.Errorf("%w：只能安装 .apk 文件", ErrInvalid)
	}
	local, cleanup, err := s.stage(r, "app.apk")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return eachPhone(ids, func(id string) BatchResult {
		if _, err := s.installStaged(ctx, id, local); err != nil {
			return BatchResult{ID: id, Error: err.Error()}
		}
		return BatchResult{ID: id, OK: true}
	}), nil
}

// UploadFileMany 给多台手机传同一个文件（图片视频进相册，其他进下载目录）。
func (s *Service) UploadFileMany(ctx context.Context, ids []string, dirKey, fileName string, r io.Reader) ([]BatchResult, error) {
	if err := checkBatchIDs(ids); err != nil {
		return nil, err
	}
	dir, ok := UploadDirs[dirKey]
	if !ok {
		return nil, fmt.Errorf("%w：不支持的目录 %q", ErrInvalid, dirKey)
	}
	safe, err := safeFileName(fileName)
	if err != nil {
		return nil, err
	}
	local, cleanup, err := s.stage(r, "file")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return eachPhone(ids, func(id string) BatchResult {
		remote, err := s.uploadStaged(ctx, id, dir, safe, local)
		if err != nil {
			return BatchResult{ID: id, Error: err.Error()}
		}
		return BatchResult{ID: id, OK: true, Path: remote}
	}), nil
}
