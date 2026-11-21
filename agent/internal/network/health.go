package network

import (
	"context"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// 网络模块的宿主机健康检查（2026-11-15 断网事故后加，踩坑 #34）：
// 透明代理靠一条策略路由（fwmark 0x1b5 → 表 233，由 host/network.sh 加）。它被删了（例如 systemd-networkd 重启）
// 所有云手机就断网。管家程序每 30 秒查一次，不在就马上补回并记日志；mihomo 有问题也报出来，管理页显示。
// （auto-update 每 10 分钟的 network.sh heal 是第二道保险。）

// 和 host/network.sh 里的 MARK、TABLE 一致。
const (
	policyMark  = "0x1b5"
	policyTable = "233"
)

// Health 最近一次检查结果。
type Health struct {
	OK        bool       `json:"ok"`
	Problems  []string   `json:"problems,omitempty"`
	HealedAt  *time.Time `json:"healedAt,omitempty"` // 最近一次自动补回策略路由的时间
	CheckedAt time.Time  `json:"checkedAt"`
}

// HealthChecker 定期检查；Run 测试时换掉。
type HealthChecker struct {
	Syncer *Syncer
	Run    func(ctx context.Context, name string, args ...string) (string, error)

	mu     sync.Mutex
	last   Health
	healed *time.Time
}

func (h *HealthChecker) run(ctx context.Context, name string, args ...string) (string, error) {
	if h.Run != nil {
		return h.Run(ctx, name, args...)
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// Check 查一次（顺手修好能修的），返回结果。
func (h *HealthChecker) Check(ctx context.Context) Health {
	var problems []string
	// REDIRECT 模式（板子，踩坑 #101）：规则在 nft 表 prism_redir（/etc/local.d/pp-redir.start 加），没有策略路由。
	if h.Syncer != nil && h.Syncer.Opt.Redir {
		if _, err := h.run(ctx, "nft", "list", "table", "ip", "prism_redir"); err != nil {
			problems = append(problems, "网络模块的规则没加上（prism_redir 表不在）：云手机的流量不会经过代理，执行 sh /etc/local.d/pp-redir.start")
		}
		if err := h.Syncer.LastError(); err != nil {
			problems = append(problems, "网络模块（mihomo）："+err.Error())
		}
		return h.finish(problems)
	}
	// 网络模块的规则表不在（没 apply 过，或者 rollback 了）就不查策略路由：那是有意关掉的。
	if _, err := h.run(ctx, "nft", "list", "table", "ip", "prism_phone"); err == nil {
		rules, _ := h.run(ctx, "ip", "rule", "list")
		if !strings.Contains(rules, "fwmark "+policyMark+" lookup "+policyTable) {
			if out, err := h.run(ctx, "ip", "rule", "add", "fwmark", policyMark, "lookup", policyTable, "priority", "9233"); err != nil {
				problems = append(problems, "网络模块的策略路由不见了，自动补回失败："+strings.TrimSpace(out)+"（云手机上不了网，执行 bash host/network.sh heal）")
			} else {
				now := time.Now().UTC()
				h.mu.Lock()
				h.healed = &now
				h.mu.Unlock()
				log.Printf("网络模块：策略路由被删了（云手机会断网），已自动补回")
			}
		}
		routes, _ := h.run(ctx, "ip", "route", "show", "table", policyTable)
		if !strings.Contains(routes, "local default") {
			if out, err := h.run(ctx, "ip", "route", "replace", "local", "0.0.0.0/0", "dev", "lo", "table", policyTable); err != nil {
				problems = append(problems, "网络模块的本机路由不见了，自动补回失败："+strings.TrimSpace(out))
			} else {
				log.Printf("网络模块：表 %s 的本机路由被删了，已自动补回", policyTable)
			}
		}
	} else {
		problems = append(problems, "网络模块的规则没加上（prism_phone 表不在）：云手机的流量不会经过代理，执行 bash host/network.sh apply")
	}
	if h.Syncer != nil {
		if err := h.Syncer.LastError(); err != nil {
			problems = append(problems, "网络模块（mihomo）："+err.Error())
		}
	}
	return h.finish(problems)
}

func (h *HealthChecker) finish(problems []string) Health {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = Health{OK: len(problems) == 0, Problems: problems, HealedAt: h.healed, CheckedAt: time.Now().UTC()}
	return h.last
}

// Last 最近一次结果（还没查过返回 nil）。
func (h *HealthChecker) Last() *Health {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last.CheckedAt.IsZero() {
		return nil
	}
	l := h.last
	return &l
}

// Loop 一直查，直到 ctx 结束。
func (h *HealthChecker) Loop(ctx context.Context, every time.Duration) {
	for {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		h.Check(c)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
