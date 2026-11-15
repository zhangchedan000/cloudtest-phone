package phones

import (
	"context"
	"strings"
	"sync"
	"time"
)

// 上网健康检查（2026-11-15 断网事故后加，踩坑 #34）：
// 后台定期从每台运行中的手机里连一次外网，结果在管理页显示，连不上就标红。
// 以前只测功能不测外网，断了一个小时才被用户发现。
//
// 做法：在容器里用 nc 发一个 HTTP 请求到谷歌的连通性检测地址（安卓自己检测网络也用它），返回 204 = 能上网。
// 这个请求和手机里其他流量一样经网络模块、走这台手机自己的代理，所以也能发现「这台的代理挂了」。

// NetStatus 一台手机最近一次上网检查的结果。
type NetStatus struct {
	OK    bool      `json:"ok"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
}

const netProbe = `printf 'GET /generate_204 HTTP/1.1\r\nHost: connectivitycheck.gstatic.com\r\nConnection: close\r\n\r\n' | nc -w 10 connectivitycheck.gstatic.com 80 2>&1 | head -1`

// CheckInternet 从这台手机里试连外网。设备没在运行返回 nil。
func (s *Service) CheckInternet(ctx context.Context, id string) *NetStatus {
	if s.State(ctx, id) != "running" {
		return nil
	}
	out, err := s.run.Run(ctx, "docker", "exec", containerName(id), "sh", "-c", netProbe)
	line := strings.TrimSpace(out)
	st := NetStatus{At: s.now().UTC()}
	switch {
	case err == nil && strings.Contains(line, " 204"):
		st.OK = true
	case line == "" || strings.Contains(line, "Timeout"):
		st.Error = "连不上外网（超时）：检查这台的代理，或者网络模块（mihomo、策略路由）"
	default:
		st.Error = "连不上外网：" + line
	}
	return &st
}

// SampleInternet 检查一轮所有运行中的手机（最多同时 4 台）。
func (s *Service) SampleInternet(ctx context.Context) {
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, p := range s.store.Snapshot().Phones {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if st := s.CheckInternet(c, id); st != nil {
				s.netStat.put(id, *st)
			} else {
				s.netStat.drop(id)
			}
		}(p.ID)
	}
	wg.Wait()
}

// RunInternetChecker 一直检查，直到 ctx 结束。
func (s *Service) RunInternetChecker(ctx context.Context, every time.Duration) {
	for {
		s.SampleInternet(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
