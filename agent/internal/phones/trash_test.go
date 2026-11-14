package phones

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrash(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	ctx := context.Background()
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	_ = os.MkdirAll(filepath.Join(svc.cfg.DataDir, "phones", p.ID, "data"), 0o755)
	_ = os.WriteFile(filepath.Join(svc.cfg.DataDir, "phones", p.ID, "data", "x"), make([]byte, 2<<20), 0o644)
	svc.now = func() time.Time { return time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local) }
	if err := svc.Reset(ctx, p.ID, ResetRequest{}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.ListTrash()
	if err != nil || len(items) != 1 || items[0].Kind != "reset" || items[0].PhoneID != p.ID || items[0].SizeMB < 1.9 {
		t.Fatalf("回收站不对：%+v %v", items, err)
	}
	if err := svc.DeleteTrash("../../etc"); !errors.Is(err, ErrInvalid) {
		t.Fatal("带路径的名字应拒绝")
	}
	svc.now = func() time.Time { return time.Date(2026, 10, 20, 8, 0, 0, 0, time.Local) }
	if n, _ := svc.PurgeTrash(30 * 24 * time.Hour); n != 0 {
		t.Fatal("没到 30 天不该删")
	}
	svc.now = func() time.Time { return time.Date(2026, 11, 5, 8, 0, 0, 0, time.Local) }
	if n, _ := svc.PurgeTrash(30 * 24 * time.Hour); n != 1 {
		t.Fatal("超过 30 天应删掉")
	}
	if items, _ := svc.ListTrash(); len(items) != 0 {
		t.Fatalf("应该空了：%+v", items)
	}
}
