package phones

import (
	"context"
	"time"
)

// 时区 / 语言 / SIM 国家「跟随代理」靠的是代理的检测结果（出口 IP 在哪个国家、哪个时区）。
// 以前代理没点过「检测」就开机，结果是空的，时区就退回默认（美东），出口却在香港——一眼差异项。
// 现在开机和切换代理前，代理没检测过或结果太旧，就自动检测一次。

// proxyCheckMaxAge 检测结果多久算旧（出口 IP 可能会变）。
const proxyCheckMaxAge = 6 * time.Hour

// SetProxyChecker 接上代理检测（由 API 层提供：检测并把结果存进 store）。
func (s *Service) SetProxyChecker(fn func(ctx context.Context, proxyID string) error) {
	s.proxyChecker = fn
}

// ensureProxyChecked 这台手机当前代理的检测结果没有 / 失败 / 太旧，就重新检测一次。
// 检测失败不挡开机（netGuard 才管能不能上网），只是时区会退回手动 / 默认值。
func (s *Service) ensureProxyChecked(ctx context.Context, proxyID string) {
	if s.proxyChecker == nil || proxyID == "" || proxyID == DirectDevProxyID {
		return
	}
	st := s.store.Snapshot()
	px := st.Proxy(proxyID)
	if px == nil {
		return
	}
	if c := px.Check; c != nil && c.OK && c.Timezone != "" && time.Since(c.CheckedAt) < proxyCheckMaxAge {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_ = s.proxyChecker(cctx, proxyID)
}
