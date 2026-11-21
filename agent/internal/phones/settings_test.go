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
	appliedSet := strings.Join(func() []string {
		if got == nil {
			return nil
		}
		return got.Applied
	}(), ",")
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
		"dumpsys battery set present 1",
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

func TestBatteryReseedsAfterStateLost(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	// 模拟 agent 重启：内存状态没了，但手机还在运行。
	svc.batt.drop(p.ID)
	svc.driftBattery(context.Background(), p.ID)
	if svc.batt.get(p.ID) == nil {
		t.Fatal("运行中但状态丢失时，漂移应从档案重新播种")
	}
}

func TestNoBootBreakingHardwareProps(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	args := strings.Join(svc.BootArgs(*mustPhone(t, svc, p.ID), svc.store.Snapshot()), " ")
	// 绝不能用启动参数设 ro.hardware / ro.board.platform（会开不了机）
	if strings.Contains(args, "ro.hardware=") || strings.Contains(args, "ro.board.platform=") {
		t.Fatalf("不该用启动参数设 ro.hardware / ro.board.platform：\n%s", args)
	}
}

func TestParseFingerprint(t *testing.T) {
	product, id, inc, ok := parseFingerprint("POCO/xaga_global/xaga:14/UP1A.231005.007/OS2.0.3.0.ULOMIXM:user/release-keys")
	if !ok || product != "xaga_global" || id != "UP1A.231005.007" || inc != "OS2.0.3.0.ULOMIXM" {
		t.Fatalf("解析不对：%q %q %q %v", product, id, inc, ok)
	}
	if _, _, _, ok := parseFingerprint(""); ok {
		t.Fatal("空字符串应 ok=false")
	}
	if _, _, _, ok := parseFingerprint("garbage"); ok {
		t.Fatal("格式不对应 ok=false")
	}
}

func TestFingerprintPropsConsistent(t *testing.T) {
	// 测兜底：没有真机分区数据（buildProps）、只有真实主指纹时，各分区统一盖成这个指纹，不漏 redroid 默认值。
	// （有真机分区数据的机型照搬真机，见 TestBuildPropsUseRealPartitionLayout）
	svc, _, _ := newTestService(t, true, "software")
	svc.model("poco-x4-gt").BuildProps = nil
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	args := svc.BootArgs(*mustPhone(t, svc, p.ID), svc.store.Snapshot())
	joined := strings.Join(args, " ")
	// poco-x4-gt 档案填了真实 stock 指纹，应直接用它、各分区一致。
	wantFP := "POCO/xaga_global/xaga:14/UP1A.231005.007/OS2.0.3.0.ULOMIXM:user/release-keys"
	for _, part := range []string{"ro.build.fingerprint", "ro.system.build.fingerprint", "ro.vendor.build.fingerprint", "ro.product.build.fingerprint", "ro.odm.build.fingerprint", "ro.bootimage.build.fingerprint"} {
		if !strings.Contains(joined, part+"="+wantFP) {
			t.Fatalf("%s 没覆盖成真实一致指纹：\n%s", part, joined)
		}
	}
	// 从指纹派生：真实产品名、buildId、incremental、flavor。
	for _, want := range []string{"ro.product.name=xaga_global", "ro.build.id=UP1A.231005.007", "ro.build.version.incremental=OS2.0.3.0.ULOMIXM", "ro.build.flavor=xaga_global-user", "ro.build.host=android-build", "ro.build.user=android"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少 %q", want)
		}
	}
	// 主分区的 ro.product.*.name 也要盖（默认是 redroid_arm64_only，机型字段只设了 .device）。
	for _, p := range []string{"system", "vendor", "odm", "product"} {
		if !strings.Contains(joined, "ro.product."+p+".name=xaga_global") {
			t.Fatalf("主分区 ro.product.%s.name 没盖成 xaga_global：\n%s", p, joined)
		}
	}
	// 容易漏的分区（system_ext / vendor_dlkm / odm_dlkm / system_dlkm）也要覆盖：
	// 指纹、各分区 build.id/tags/type/incremental、以及 ro.product.*.{brand,model,device,name}。
	for _, p := range []string{"system_ext", "vendor_dlkm", "odm_dlkm", "system_dlkm"} {
		for _, want := range []string{
			"ro." + p + ".build.fingerprint=" + wantFP,
			"ro." + p + ".build.id=UP1A.231005.007",
			"ro." + p + ".build.tags=release-keys",
			"ro." + p + ".build.type=user",
			"ro.product." + p + ".brand=POCO",
			"ro.product." + p + ".name=xaga_global",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("分区 %s 没覆盖 %q：\n%s", p, want, joined)
			}
		}
	}
	// 所有 ro.product.* 和 *.build.* 属性里都不能有 redroid / userdebug / test-keys / frank / build1 痕迹
	// （androidboot.redroid_* 是 redroid 启动配置、不是 getprop 属性，/proc/cmdline 也不暴露，排除）
	var idArgs []string
	for _, a := range args {
		if strings.HasPrefix(a, "ro.build.") || strings.HasPrefix(a, "ro.product.") || strings.Contains(a, ".build.fingerprint") || strings.Contains(a, ".build.id") || strings.Contains(a, ".build.tags") || strings.Contains(a, ".build.type") || strings.Contains(a, ".build.version.incremental") {
			idArgs = append(idArgs, a)
		}
	}
	bj := strings.Join(idArgs, " ")
	for _, bad := range []string{"redroid", "userdebug", "test-keys", "build1", "eng.frank"} {
		if strings.Contains(bj, bad) {
			t.Fatalf("身份/build 属性里还有痕迹 %q：\n%s", bad, bj)
		}
	}
	// 每个参数都不能含空格（会被截断）
	for _, a := range args {
		if strings.Contains(a, " ") {
			t.Fatalf("启动参数含空格会被截断：%q", a)
		}
	}
}

func TestFingerprintFallbackWhenNoProfileValue(t *testing.T) {
	// xiaomi-12t 档案没填真实指纹 → 用 Redroid 镜像真实 buildId/incremental 兜底拼一致指纹。
	svc, _, _ := newTestService(t, true, "software")
	m := svc.model("xiaomi-12t")
	m.Fingerprints, m.BuildProps = nil, nil // 模拟没有真机数据的机型
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "xiaomi-12t", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	joined := strings.Join(svc.BootArgs(*mustPhone(t, svc, p.ID), svc.store.Snapshot()), " ")
	wantFP := "Xiaomi/plato/plato:14/UD2A.240505.001.W1/eng.frank.20240527.152423:user/release-keys"
	if !strings.Contains(joined, "ro.build.fingerprint="+wantFP) || !strings.Contains(joined, "ro.vendor.build.fingerprint="+wantFP) {
		t.Fatalf("兜底指纹不对：\n%s", joined)
	}
}

func TestRedroidFPS(t *testing.T) {
	for in, want := range map[any]int{144: 120, 120: 120, 90: 90, 60: 60, float64(165): 120, "144": 120, 0: 60} {
		if got := redroidFPS(in); got != want {
			t.Errorf("redroidFPS(%v)=%d 应为 %d", in, got, want)
		}
	}
}

// 有真机分区数据（buildProps）的机型：各分区照搬真机原样，不再统一成同一个机型（踩坑 #52）。
func TestBuildPropsUseRealPartitionLayout(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "xiaomi-13t", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	args := svc.BootArgs(*mustPhone(t, svc, p.ID), svc.store.Snapshot())
	joined := " " + strings.Join(args, " ") + " "
	for _, want := range []string{
		"ro.build.fingerprint=Xiaomi/aristotle_global/aristotle:14/UP1A.230905.011/V816.0.2.0.UMFMIXM:user/release-keys",
		"ro.product.system.brand=Android", "ro.product.system.model=mainline", // 小米的 system 分区是通用镜像
		"ro.product.product.name=miproduct_aristotle_global",
		"ro.odm.build.fingerprint=Xiaomi/aristotle_global/aristotle:12/SP1A.210812.016/V816.0.2.0.UMFMIXM:user/release-keys", // odm 停在出厂版本
		"ro.product.vendor.model=2306EPN60G",
	} {
		if !strings.Contains(joined, " "+want+" ") {
			t.Errorf("缺少真机分区属性 %q", want)
		}
	}
	if strings.Contains(joined, " ro.product.system.brand=Xiaomi ") {
		t.Error("system 分区不该被改成机型品牌（真机是 Android）")
	}
}

// 所有机型 × 安卓版本：启动参数里同一个属性只能出现一次、不能带空格、身份属性不能残留 redroid 痕迹。
func TestBootArgsNoDuplicateProps(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	for _, m := range svc.models {
		for _, v := range []int{14, 15} {
			p, err := svc.Create(CreateRequest{Name: "A", ModelID: m.ID, AndroidVersion: v, ProxyID: DirectDevProxyID})
			if err != nil {
				continue // 这个机型不支持这个版本
			}
			seen := map[string]string{}
			for _, a := range svc.BootArgs(*mustPhone(t, svc, p.ID), svc.store.Snapshot()) {
				k, _, _ := strings.Cut(a, "=")
				if old, dup := seen[k]; dup {
					t.Errorf("%s/%d：%s 重复（%q 和 %q）", m.ID, v, k, old, a)
				}
				seen[k] = a
				if strings.Contains(a, " ") {
					t.Errorf("%s/%d：参数带空格 %q", m.ID, v, a)
				}
				if (strings.HasPrefix(a, "ro.product.") || strings.Contains(a, ".build.")) && strings.Contains(strings.ToLower(a), "redroid") {
					t.Errorf("%s/%d：身份属性残留 redroid：%q", m.ID, v, a)
				}
			}
		}
	}
}

// 踩坑 #118：语言开机前就放进 ro.product.locale；安卓 15+ 开机后语言要变也不能软重启框架（会崩溃循环）。
func TestLocaleNoFrameworkRestartOnA15Plus(t *testing.T) {
	for _, ver := range []int{15, 16} {
		svc, _, _ := newTestService(t, true, "software")
		svc.cfg.Images[ver] = "redroid:x"
		p, err := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: ver, ProxyID: DirectDevProxyID})
		if err != nil {
			t.Fatal(err)
		}
		_ = svc.Save(p.ID, SaveRequest{Fields: map[string]store.FieldSetting{"locale": {Mode: "manual", Value: "en-US"}}})
		args := strings.Join(svc.BootArgs(*mustPhone(t, svc, p.ID), svc.store.Snapshot()), " ")
		if !strings.Contains(args, "ro.product.locale=en-US") {
			t.Fatalf("A%d 开机参数应带默认语言：%s", ver, args)
		}
		if err := svc.Start(context.Background(), p.ID); err != nil {
			t.Fatal(err)
		}
		calls := strings.Join(svc.run.(*fakeRunner).calls, "\n")
		if strings.Contains(calls, "ctl.restart zygote") {
			t.Fatalf("A%d 不该软重启框架：\n%s", ver, calls)
		}
		if got := svc.settings.get(p.ID); got == nil || got.State != "applied" || !strings.Contains(strings.Join(got.Applied, ","), "locale") {
			t.Fatalf("A%d 设置结果不对：%+v", ver, got)
		}
	}
}
