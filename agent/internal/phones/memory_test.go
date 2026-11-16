package phones

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMemLimitFollowsModel(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	svc.cfg.MemFollowModel = true
	got := svc.memLimitMB(map[string]any{"memTotal": float64(8)})
	if got != 8_000_000_000/(1<<20) && got >= svc.cfg.MemoryMB { // 宿主机内存太小时会被封顶
		t.Fatalf("8GB 机型上限应约 7629 MiB：%d", got)
	}
	svc.cfg.MemFollowModel = false
	if got := svc.memLimitMB(map[string]any{"memTotal": float64(8)}); got != svc.cfg.MemoryMB {
		t.Fatalf("关掉跟随机型时用固定值：%d", got)
	}
	svc.cfg.MemFollowModel = true
	if got := svc.memLimitMB(map[string]any{}); got != svc.cfg.MemoryMB && got > svc.cfg.MemoryMB {
		t.Fatalf("没填内存用固定值：%d", got)
	}
}

func TestMemoryBudgetBlocksThirdPhone(t *testing.T) {
	svc, fd, _ := newTestService(t, true, "software")
	svc.cfg.MemFollowModel = true
	svc.cfg.HostMemMB = 24 * 1024 // 阿什本：24GB
	svc.cfg.MaxRunning = 10
	ctx := context.Background()
	var ids []string
	for i := 0; i < 3; i++ {
		p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
		ids = append(ids, p.ID)
	}
	if err := svc.Start(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	err := svc.Start(ctx, ids[2])
	if !errors.Is(err, ErrBlocked) || !strings.Contains(err.Error(), "内存不够") {
		t.Fatalf("24GB 机器开第 3 台 8GB 手机应被拦：%v", err)
	}
	if spec := fd.created["pp-"+ids[0]]; spec.MemoryMB < 7000 {
		t.Fatalf("容器内存上限应按机型 8GB：%d", spec.MemoryMB)
	}
}
