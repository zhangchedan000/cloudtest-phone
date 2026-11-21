package phones

import (
	"os"
	"path/filepath"
	"testing"
)

// 启动时只清上传 / 剪贴板临时文件，别的东西不碰。
func TestCleanStaging(t *testing.T) {
	svc, _, _ := newTestService(t, true, "")
	dir := filepath.Join(svc.cfg.DataDir, "tmp")
	_ = os.MkdirAll(dir, 0o700)
	for _, n := range []string{"upload-1-a.apk", "clip-2", "keep.txt"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	n, err := svc.CleanStaging()
	if err != nil || n != 2 {
		t.Fatalf("应清 2 个：%d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatal("不该删别的文件")
	}
}
