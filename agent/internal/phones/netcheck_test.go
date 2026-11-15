package phones

import (
	"context"
	"strings"
	"testing"
)

// probeRunner 假装手机里的 nc：online=true 返回 204，否则超时。
type probeRunner struct {
	fakeRunner
	online bool
}

func (r *probeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	base, err := r.fakeRunner.Run(ctx, name, args...)
	if len(args) >= 5 && args[2] == "sh" && args[4] == netProbe {
		if r.online {
			return "HTTP/1.1 204 No Content\r\n", nil
		}
		return "nc: Timeout\n", nil
	}
	return base, err
}

func TestInternetCheck(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	ctx := context.Background()
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	r := &probeRunner{online: true}
	svc.run = r
	if svc.CheckInternet(ctx, p.ID) != nil {
		t.Fatal("没开机不该检查")
	}
	if err := svc.Start(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	svc.SampleInternet(ctx)
	view := func() *NetStatus {
		for _, v := range svc.List(ctx) {
			if v.ID == p.ID {
				return v.Net
			}
		}
		return nil
	}
	if n := view(); n == nil || !n.OK {
		t.Fatalf("能上网应显示 OK：%+v", n)
	}
	r.online = false
	svc.SampleInternet(ctx)
	if n := view(); n == nil || n.OK || !strings.Contains(n.Error, "超时") {
		t.Fatalf("上不了网应报错：%+v", n)
	}
	_ = svc.Stop(ctx, p.ID)
	svc.SampleInternet(ctx)
	if n := view(); n != nil {
		t.Fatalf("关机后不该再显示：%+v", n)
	}
}
