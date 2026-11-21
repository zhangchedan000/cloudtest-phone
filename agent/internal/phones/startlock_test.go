package phones

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 两台同时开机：预占期间第二台不能拿到同一组 binder，同一台不能并发开两次，台数上限算上正在开机的。
func TestReserveStartDistinctSlots(t *testing.T) {
	svc, _, st := newTestService(t, true, "")
	_ = st.Update(func(s *store.State) error {
		s.Phones = append(s.Phones, store.Phone{ID: "a1"}, store.Phone{ID: "b2"}, store.Phone{ID: "c3"})
		return nil
	})
	ctx := context.Background()
	snap := st.Snapshot()
	s1, _, rel1, err := svc.reserveStart(ctx, snap, "a1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.reserveStart(ctx, snap, "a1", 0); !errors.Is(err, ErrBlocked) {
		t.Fatalf("同一台并发开机应被挡：%v", err)
	}
	s2, _, rel2, err := svc.reserveStart(ctx, snap, "b2", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s1 == s2 {
		t.Fatalf("两台同时开机拿到了同一组 binder：%d", s1)
	}
	// MaxRunning=2：两台正在开机，第三台要被挡
	if _, _, _, err := svc.reserveStart(ctx, snap, "c3", 0); !errors.Is(err, ErrBlocked) {
		t.Fatalf("台数上限没算正在开机的：%v", err)
	}
	rel1()
	rel2()
	if _, _, rel3, err := svc.reserveStart(ctx, snap, "c3", 0); err != nil {
		t.Fatalf("释放后应能开：%v", err)
	} else {
		rel3()
	}
}

// 不认识的 id（比如带 ../）删除时不能碰文件系统。
func TestDeleteUnknownIDKeepsFiles(t *testing.T) {
	svc, _, _ := newTestService(t, true, "")
	victim := filepath.Join(svc.cfg.DataDir, "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), "../victim"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("应返回找不到：%v", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("目录被挪走了：%v", err)
	}
}
