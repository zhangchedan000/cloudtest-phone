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
	appliedSet := strings.Join(func() []string { if got == nil { return nil }; return got.Applied }(), ",")
	if got == nil || got.State != "applied" || !strings.Contains(appliedSet, "timezone") || !strings.Contains(appliedSet, "locale") {
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

func TestStartingUntilSettingsDone(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	ctx := context.Background()
	if err := svc.Start(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	svc.settings.set(p.ID, SettingsStatus{State: "pending"})
	if got := svc.State(ctx, p.ID); got != "starting" {
		t.Fatalf("开机后设置没做完应算开机中：%s", got)
	}
	svc.settings.set(p.ID, SettingsStatus{State: "applied"})
	if got := svc.State(ctx, p.ID); got != "running" {
		t.Fatalf("设置做完应算运行中：%s", got)
	}
}

func TestBootArgsHaveDisguiseProps(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	args := strings.Join(svc.BootArgs(*mustPhone(t, svc, p.ID), svc.store.Snapshot()), " ")
	want := []string{
		"ro.debuggable=0", "ro.build.type=user", "ro.build.tags=release-keys",
		"ro.boot.verifiedbootstate=green", "ro.boot.flash.locked=1",
		"ro.boot.veritymode=enforcing", "ro.boot.vbmeta.device_state=locked", "ro.secure=1",
	}
	for _, w := range want {
		if !strings.Contains(args, w) {
			t.Fatalf("启动参数缺少伪装属性 %q：\n%s", w, args)
		}
	}
	// 不能传空值属性
	if strings.Contains(args, "security_patch= ") || strings.HasSuffix(args, "=") {
		t.Fatalf("不该出现空值属性：\n%s", args)
	}
}

func TestSettingSinkWritesAndroidIdAndNames(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	_ = svc.Save(p.ID, SaveRequest{Fields: map[string]store.FieldSetting{
		"androidId":    {Mode: "manual", Value: "0123456789abcdef"},
		"bluetoothMac": {Mode: "manual", Value: "aa:bb:cc:dd:ee:ff"},
	}})
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(svc.run.(*fakeRunner).calls, "\n")
	for _, want := range []string{
		"settings put secure android_id 0123456789abcdef",
		"settings put secure bluetooth_address aa:bb:cc:dd:ee:ff",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("缺少 setting 写入 %q：\n%s", want, calls)
		}
	}
	// 含控制字符的值要被挡掉
	if !safeSettingValue("POCO X4 GT") || safeSettingValue("bad\nvalue") || safeSettingValue("") {
		t.Fatal("safeSettingValue 判断不对")
	}
}

func TestBatterySinkAppliesViaDumpsys(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	_ = svc.Save(p.ID, SaveRequest{Fields: map[string]store.FieldSetting{
		"batteryLevel":    {Mode: "manual", Value: 61},
		"batteryCharging": {Mode: "manual", Value: "usb"},
		"batteryTemp":     {Mode: "manual", Value: 32.5},
		"batteryHealth":   {Mode: "manual", Value: "good"},
	}})
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(svc.run.(*fakeRunner).calls, "\n")
	for _, want := range []string{
		"dumpsys battery set level 61",
		"dumpsys battery set usb 1",
		"dumpsys battery set status 2",
		"dumpsys battery set temp 325",
		"dumpsys battery set health 2",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("缺少电池命令 %q：\n%s", want, calls)
		}
	}
}

func TestBatteryDriftMovesAndClamps(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	// Start 后应已播种电池状态（poco 档案电池是 random 模式，必有值）。
	if svc.batt.get(p.ID) == nil {
		t.Fatal("开机后应播种电池状态")
	}
	// 充电场景：电量只升不超 100
	svc.batt.put(p.ID, batteryState{Level: 99, TempDeci: 300, Charging: "ac"})
	for i := 0; i < 30; i++ {
		svc.driftBattery(context.Background(), p.ID)
	}
	st := svc.batt.get(p.ID)
	if st == nil || st.Level > 100 || st.Level < 99 {
		t.Fatalf("充电漂移越界：%+v", st)
	}
	// 用电场景：电量只降不低于 5
	svc.batt.put(p.ID, batteryState{Level: 6, TempDeci: 300, Charging: "none"})
	for i := 0; i < 50; i++ {
		svc.driftBattery(context.Background(), p.ID)
	}
	st = svc.batt.get(p.ID)
	if st == nil || st.Level < 5 || st.Level > 6 {
		t.Fatalf("用电漂移越界：%+v", st)
	}
	// 温度始终在合理范围
	if st.TempDeci < 230 || st.TempDeci > 430 {
		t.Fatalf("温度越界：%+v", st)
	}
}
