package network

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeHost 假装宿主机：记录 ip rule / route 的增删。
type fakeHost struct {
	table, rule, route bool
	addFails           bool
	calls              []string
}

func (f *fakeHost) run(_ context.Context, name string, args ...string) (string, error) {
	cmd := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	switch {
	case strings.HasPrefix(cmd, "nft list table"):
		if !f.table {
			return "Error: No such file or directory", errors.New("exit 1")
		}
		return "table ip prism_phone {}", nil
	case cmd == "ip rule list":
		if f.rule {
			return "0: from all lookup local\n9233: from all fwmark 0x1b5 lookup 233\n", nil
		}
		return "0: from all lookup local\n", nil
	case strings.HasPrefix(cmd, "ip rule add"):
		if f.addFails {
			return "RTNETLINK answers: Operation not permitted", errors.New("exit 2")
		}
		f.rule = true
		return "", nil
	case strings.HasPrefix(cmd, "ip route show table"):
		if f.route {
			return "local default dev lo scope host", nil
		}
		return "", nil
	case strings.HasPrefix(cmd, "ip route replace"):
		f.route = true
		return "", nil
	}
	return "", nil
}

func TestHealthHealsPolicyRoute(t *testing.T) {
	f := &fakeHost{table: true, rule: true, route: true}
	h := &HealthChecker{Run: f.run}
	if st := h.Check(context.Background()); !st.OK || st.HealedAt != nil {
		t.Fatalf("一切正常时：%+v", st)
	}
	// 策略路由被删了（踩坑 #34）：自动补回，结果仍然 OK，但记下补回时间
	f.rule, f.route = false, false
	st := h.Check(context.Background())
	if !st.OK || st.HealedAt == nil || !f.rule || !f.route {
		t.Fatalf("应该自动补回：%+v %v", st, f.calls)
	}
	// 补不回来：报问题
	f.rule, f.addFails = false, true
	if st := h.Check(context.Background()); st.OK || len(st.Problems) != 1 || !strings.Contains(st.Problems[0], "补回失败") {
		t.Fatalf("补不回来应报问题：%+v", st)
	}
	// 网络模块的规则表不在：报问题，不去加策略路由
	f2 := &fakeHost{}
	h2 := &HealthChecker{Run: f2.run}
	st = h2.Check(context.Background())
	if st.OK || !strings.Contains(strings.Join(st.Problems, ""), "prism_phone") {
		t.Fatalf("规则表不在应报问题：%+v", st)
	}
	for _, c := range f2.calls {
		if strings.HasPrefix(c, "ip rule add") {
			t.Fatal("规则表不在时不该加策略路由")
		}
	}
	if h2.Last() == nil {
		t.Fatal("Last 应该有结果")
	}
}

// REDIRECT 模式（板子）：只查 prism_redir 表，不查策略路由，也不误报 prism_phone。
func TestHealthRedirMode(t *testing.T) {
	var calls []string
	exists := true
	h := &HealthChecker{Syncer: &Syncer{Opt: Options{Redir: true}}, Run: func(_ context.Context, name string, args ...string) (string, error) {
		c := name + " " + strings.Join(args, " ")
		calls = append(calls, c)
		if c == "nft list table ip prism_redir" && !exists {
			return "", errors.New("no such table")
		}
		return "", nil
	}}
	if st := h.Check(context.Background()); !st.OK {
		t.Fatalf("redir 表在时应 OK：%+v %v", st, calls)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "ip ") || strings.Contains(c, "prism_phone") {
			t.Fatalf("redir 模式不该查策略路由 / prism_phone：%v", calls)
		}
	}
	exists = false
	if st := h.Check(context.Background()); st.OK || !strings.Contains(strings.Join(st.Problems, ""), "prism_redir") {
		t.Fatalf("redir 表不在应报问题：%+v", st)
	}
}
