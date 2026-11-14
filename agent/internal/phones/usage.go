package phones

import (
	"context"
	"sync"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/docker"
)

// 资源占用：后台每 10 秒采一次运行中设备的 CPU、内存（docker stats），管理页显示。
// 采样放在后台，列表接口不用等 Docker（stats 每次要采样约 1 秒）。

type usageReader interface {
	ContainerUsage(ctx context.Context, name string) (docker.Usage, error)
}

// SampleUsage 采一轮；Docker 不支持就什么都不做。
func (s *Service) SampleUsage(ctx context.Context) {
	ur, ok := s.docker.(usageReader)
	if !ok {
		return
	}
	var wg sync.WaitGroup
	for _, p := range s.store.Snapshot().Phones {
		if s.State(ctx, p.ID) == "stopped" {
			s.usage.put(p.ID, docker.Usage{})
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if u, err := ur.ContainerUsage(c, containerName(id)); err == nil {
				s.usage.put(id, u)
			}
		}(p.ID)
	}
	wg.Wait()
}

// RunUsageSampler 一直采样，直到 ctx 结束。
func (s *Service) RunUsageSampler(ctx context.Context, every time.Duration) {
	for {
		s.SampleUsage(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
