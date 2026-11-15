package phones

import (
	"context"
	"fmt"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 一台手机挂多个代理、随时切换（2026-11-15 用户要求「一台手机可以用多个 IP」）。
// 切换 = 改「当前在用」的那个：网络模块马上按新代理分流；开着的手机如果时区跟随代理，马上换成新代理的时区。
// （语言要重启安卓框架才生效，切换时不动，下次开机自动跟上。）

// ProxySwitchResult 切换结果。
type ProxySwitchResult struct {
	ProxyID  string `json:"proxyId"`
	Timezone string `json:"timezone,omitempty"` // 开着的手机：切换后设上的时区
	Warning  string `json:"warning,omitempty"`  // 新代理没有出口等（已经切过去了，但手机可能上不了网）
}

// SwitchProxy 把这台手机当前用的代理换成 proxyID（不在它的代理列表里就顺便加进去）。
func (s *Service) SwitchProxy(ctx context.Context, id, proxyID string) (ProxySwitchResult, error) {
	res := ProxySwitchResult{ProxyID: proxyID}
	err := s.store.Update(func(st *store.State) error {
		p, err := st.Phone(id)
		if err != nil {
			return err
		}
		if proxyID == "" || (proxyID != DirectDevProxyID && st.Proxy(proxyID) == nil) {
			return fmt.Errorf("%w：代理不存在", ErrInvalid)
		}
		if proxyID == DirectDevProxyID && !s.cfg.DevHost {
			return fmt.Errorf("%w：正式机器不能用直连", ErrInvalid)
		}
		if len(p.ProxyIDs) == 0 && p.ProxyID != "" {
			p.ProxyIDs = []string{p.ProxyID}
		}
		if !contains(p.ProxyIDs, proxyID) {
			p.ProxyIDs = append(p.ProxyIDs, proxyID)
		}
		p.ProxyID = proxyID
		return nil
	})
	if err != nil {
		return res, err
	}
	// 网络模块马上按新代理分流（netGuard 会同步一次 mihomo 配置）
	if s.netGuard != nil {
		if err := s.netGuard(id); err != nil {
			res.Warning = err.Error()
		}
	}
	if s.State(ctx, id) != "running" {
		return res, nil
	}
	st := s.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil {
		return res, nil
	}
	for _, r := range s.resolve(*p, st) {
		if r.Key != "timezone" || r.Value == nil {
			continue
		}
		tz := fmt.Sprint(r.Value)
		if !liveProps["timezone"].pattern.MatchString(tz) {
			break
		}
		name := containerName(id)
		if out, err := s.run.Run(ctx, "docker", "exec", name, "setprop", "persist.sys.timezone", tz); err != nil {
			res.Warning = fmt.Sprintf("时区没换成：%v %s", err, out)
			break
		}
		_, _ = s.run.Run(ctx, "docker", "exec", name, "cmd", "time_zone_detector", "suggest_manual_time_zone", "--zone_id", tz)
		res.Timezone = tz
	}
	return res, nil
}
