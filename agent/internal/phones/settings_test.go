package phones

import (
	"context"
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

func TestSettingsAppliedAfterBoot(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, err := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]store.FieldSetting{
		"timezone": {Mode: "manual", Value: "America/Chicago"},
		"locale":   {Mode: "manual", Value: "en-US"},
	}
	if err := svc.Save(p.ID, SaveRequest{Fields: fields}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(svc.run.(*fakeRunner).calls, "\n")
	for _, want := range []string{"setprop persist.sys.timezone America/Chicago", "setprop persist.sys.locale en-US", "setprop ctl.restart zygote"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("缺少命令 %q：\n%s", want, calls)
		}
	}
	got := svc.settings.get(p.ID)
	if got == nil || got.State != "applied" || len(got.Applied) != 2 {
		t.Fatalf("设置结果不对：%+v", got)
	}
	for _, a := range svc.BootArgs(*mustPhone(t, svc, p.ID), svc.store.Snapshot()) {
		if strings.Contains(a, "persist.sys") {
			t.Fatalf("persist 属性不该出现在启动参数里：%s", a)
		}
	}
}

func TestSettingsRejectOddValues(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	_ = svc.Save(p.ID, SaveRequest{Fields: map[string]store.FieldSetting{"timezone": {Mode: "manual", Value: "x; reboot"}}})
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(svc.run.(*fakeRunner).calls, "\n"), "reboot") {
		t.Fatal("格式不对的值不该传进容器")
	}
	if got := svc.settings.get(p.ID); got == nil || got.State != "failed" || !strings.Contains(got.Error, "格式不对") {
		t.Fatalf("应报格式不对：%+v", got)
	}
}

func mustPhone(t *testing.T, svc *Service, id string) *store.Phone {
	t.Helper()
	st := svc.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
