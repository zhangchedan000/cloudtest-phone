package phones

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

func TestBackupRestoreFlow(t *testing.T) {
	svc, _, st := newTestService(t, true, "software")
	ctx := context.Background()
	a, err := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateBackup(ctx, a.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("没开过机（没数据）不该能备份：%v", err)
	}
	// 老式目录数据
	_ = os.MkdirAll(svc.dataDir(a.ID), 0o755)
	_ = os.WriteFile(filepath.Join(svc.dataDir(a.ID), "x"), []byte("1"), 0o644)
	b, err := svc.CreateBackup(ctx, a.ID, "登录好的")
	if err != nil {
		t.Fatal(err)
	}
	if b.Kind != "dir" || b.Note != "登录好的" || b.AndroidVersion != 14 {
		t.Fatalf("%+v", b)
	}
	calls := strings.Join(svc.run.(*fakeRunner).calls, "\n")
	if !strings.Contains(calls, "tar --numeric-owner --xattrs --sparse -cpf") {
		t.Fatalf("目录数据要用 tar 备份：\n%s", calls)
	}
	list, _ := svc.ListBackups()
	if len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("%+v", list)
	}
	// 恢复到另一台同版本手机：原数据进回收站
	c, _ := svc.Create(CreateRequest{Name: "C", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	_ = os.MkdirAll(svc.dataDir(c.ID), 0o755)
	_ = os.WriteFile(filepath.Join(svc.dataDir(c.ID), "old"), []byte("1"), 0o644)
	if err := svc.RestoreBackup(ctx, c.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(svc.dataDir(c.ID), "old")); err == nil {
		t.Fatal("原来的数据应挪进回收站")
	}
	trash, _ := svc.ListTrash()
	if len(trash) == 0 {
		t.Fatal("回收站里应该有原来的数据")
	}
	// 谷歌服务设置不同不能恢复
	g, _ := svc.Create(CreateRequest{Name: "G", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	_ = st.Update(func(s *store.State) error { ph, _ := s.Phone(g.ID); ph.Gapps = true; return nil })
	if err := svc.RestoreBackup(ctx, g.ID, b.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("谷歌服务设置不同应拒绝：%v", err)
	}
	if err := svc.RestoreBackup(ctx, c.ID, "../x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("备份编号带路径应拒绝：%v", err)
	}
	if err := svc.DeleteBackup(b.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.ListBackups(); len(list) != 0 {
		t.Fatal("删了还在")
	}
}
