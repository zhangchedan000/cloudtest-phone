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
	if err := svc.checkMemoryBudget(ctx, ids[3], mine); !errors.Is(err, ErrBlocked) {
		t.Fatalf("不超卖时 32GB 只能开 3 台：%v", err)
	}
	svc.cfg.MemOvercommit = 2.5
	old := availableMB
	defer func() { availableMB = old }()
	availableMB = func() int64 { return 20 * 1024 }
	if err := svc.checkMemoryBudget(ctx, ids[3], mine); err != nil {
		t.Fatalf("超卖 2.5 倍应能开第 4 台：%v", err)
	}
	availableMB = func() int64 { return 2 * 1024 }
	if err := svc.checkMemoryBudget(ctx, ids[3], mine); !errors.Is(err, ErrBlocked) {
		t.Fatalf("机器实际只剩 2GB 时不该再开：%v", err)
	}
}
