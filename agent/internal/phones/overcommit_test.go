package phones

import (
	"context"
	"errors"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/docker"
)

// Q1（32GB）：不超卖只能开 3 台 8GB 的；超卖 2.5 倍能开 9 台，但机器实际剩的内存不够时照样不让开。
func TestMemOvercommit(t *testing.T) {
	svc, fd, _ := newTestService(t, true, "software")
	svc.cfg.MemFollowModel = true
	svc.cfg.HostMemMB = 32 * 1024
	ctx := context.Background()
	var ids []string
	for i := 0; i < 4; i++ {
		p, err := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	mine := int64(7629)
	for _, id := range ids[:3] {
		fd.created[containerName(id)] = docker.ContainerSpec{Name: containerName(id)}
		fd.running[containerName(id)] = true
	}
	if err := svc.checkMemoryBudget(ctx, ids[3], mine, nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("不超卖时 32GB 只能开 3 台：%v", err)
	}
	svc.cfg.MemOvercommit = 2.5
	old := availableMB
	defer func() { availableMB = old }()
	availableMB = func() int64 { return 20 * 1024 }
	if err := svc.checkMemoryBudget(ctx, ids[3], mine, nil); err != nil {
		t.Fatalf("超卖 2.5 倍应能开第 4 台：%v", err)
	}
	availableMB = func() int64 { return 2 * 1024 }
	if err := svc.checkMemoryBudget(ctx, ids[3], mine, nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("机器实际只剩 2GB 时不该再开：%v", err)
	}
}

// 几台一起开机：正在开机的手机（容器已经在跑）已经按 pending 算进 mine，不能再按运行中算第二次。
func TestMemBudgetPendingNotDoubleCounted(t *testing.T) {
	svc, fd, _ := newTestService(t, true, "software")
	svc.cfg.MemFollowModel = true
	svc.cfg.HostMemMB = 32 * 1024 // 能给手机用 29GB：3 台 8GB 的装得下，4 台装不下
	ctx := context.Background()
	var ids []string
	for i := 0; i < 3; i++ {
		p, err := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	for _, id := range ids[:2] {
		fd.created[containerName(id)] = docker.ContainerSpec{Name: containerName(id)}
		fd.running[containerName(id)] = true
	}
	mine := int64(7629) * 3 // 自己 + 正在开机的两台
	pending := map[string]bool{ids[0]: true, ids[1]: true}
	if err := svc.checkMemoryBudget(ctx, ids[2], mine, pending); err != nil {
		t.Fatalf("3 台刚好装得下，正在开机的不该重复算：%v", err)
	}
	if err := svc.checkMemoryBudget(ctx, ids[2], mine, nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("不排除时会重复算（旧行为）：%v", err)
	}
}
