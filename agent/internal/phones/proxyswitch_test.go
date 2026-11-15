package phones

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

func TestSwitchProxy(t *testing.T) {
	svc, _, st := newTestService(t, true, "software")
	ctx := context.Background()
	_ = st.Update(func(s *store.State) error {
		s.Proxies = append(s.Proxies,
			store.Proxy{ID: "px-la", Name: "洛杉矶", Protocol: "socks5", Address: "socks5://1.1.1.1:1080", Check: &store.ProxyCheck{OK: true, Country: "US", Timezone: "America/Los_Angeles"}},
			store.Proxy{ID: "px-ny", Name: "纽约", Protocol: "socks5", Address: "socks5://2.2.2.2:1080", Check: &store.ProxyCheck{OK: true, Country: "US", Timezone: "America/New_York"}})
		return nil
	})
	p, err := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err != nil {
		t.Fatal(err)
	}
	if got := phoneOf(t, st, p.ID); len(got.ProxyIDs) != 1 || got.ProxyIDs[0] != DirectDevProxyID {
		t.Fatalf("新建时代理列表应只有一个：%v", got.ProxyIDs)
	}
	// 关机时切换：只改记录
	if res, err := svc.SwitchProxy(ctx, p.ID, "px-la"); err != nil || res.Timezone != "" {
		t.Fatalf("关机切换：%+v %v", res, err)
	}
	got := phoneOf(t, st, p.ID)
	if got.ProxyID != "px-la" || len(got.ProxyIDs) != 2 {
		t.Fatalf("应切到洛杉矶，列表两个：%+v", got)
	}
	// 开机后切换：时区马上跟着换
	if err := svc.Start(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{}
	svc.run = r
	res, err := svc.SwitchProxy(ctx, p.ID, "px-ny")
	if err != nil || res.Timezone != "America/New_York" {
		t.Fatalf("开机切换应设上纽约时区：%+v %v", res, err)
	}
	if !strings.Contains(strings.Join(r.calls, "\n"), "setprop persist.sys.timezone America/New_York") {
		t.Fatalf("应该 setprop 时区：%v", r.calls)
	}
	// 切回列表里已有的：列表不重复
	if _, err := svc.SwitchProxy(ctx, p.ID, "px-la"); err != nil || len(phoneOf(t, st, p.ID).ProxyIDs) != 3 {
		t.Fatalf("列表不该重复：%v %v", phoneOf(t, st, p.ID).ProxyIDs, err)
	}
	if _, err := svc.SwitchProxy(ctx, p.ID, "不存在"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("不存在的代理应拒绝：%v", err)
	}
	// 保存代理列表：当前在用的不在列表里时改成第一个
	list := []string{"px-ny"}
	if err := svc.Save(p.ID, SaveRequest{ProxyIDs: &list}); err != nil {
		t.Fatal(err)
	}
	if got := phoneOf(t, st, p.ID); got.ProxyID != "px-ny" || len(got.ProxyIDs) != 1 {
		t.Fatalf("保存列表后：%+v", got)
	}
}

func phoneOf(t *testing.T, st *store.Store, id string) store.Phone {
	t.Helper()
	snap := st.Snapshot()
	p, err := snap.Phone(id)
	if err != nil {
		t.Fatal(err)
	}
	return *p
}
