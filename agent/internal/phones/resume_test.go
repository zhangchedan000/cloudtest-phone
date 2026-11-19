package phones

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// resumeRunner 模拟管家重启后的容器：可设定 /.ppfake 在不在、/proc/mounts 里已有哪些挂载。
type resumeRunner struct {
	fakeRunner
	ppfake bool
	mounts []string
}

func (r *resumeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if len(args) >= 4 && args[2] == "ls" && args[3] == fakeMount {
		if r.ppfake {
			return "", nil
		}
		return "No such file", errors.New("exit 1")
	}
	if len(args) >= 4 && args[2] == "cat" && args[3] == "/proc/mounts" {
		var b strings.Builder
		for _, m := range r.mounts {
			b.WriteString("tmpfs " + m + " tmpfs ro 0 0\n")
		}
		return b.String(), nil
	}
	r.calls = r.calls[:len(r.calls)-1]
	return r.fakeRunner.Run(ctx, name, args...)
}

func startForResume(t *testing.T) (*Service, string) {
	t.Helper()
	svc, _, _ := newTestService(t, true, "software")
	svc.cfg.HostCPUs = 4
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	// 模拟管家重启：内存里的状态全丢
	svc.settings = settingsTracker{}
	svc.fakes = fakeTracker{}
	return svc, p.ID
}

func TestResumeRunningRedoesSettingsAndTraces(t *testing.T) {
	svc, id := startForResume(t)
	r := &resumeRunner{ppfake: false, mounts: []string{"/proc/cpuinfo", "/proc/version"}}
	svc.run = r
	if n := svc.ResumeRunning(context.Background()); n != 1 {
		t.Fatalf("应补做 1 台，实际 %d", n)
	}
	joined := strings.Join(r.calls, "\n")
	for _, want := range []string{"umount -l /data/.ppi", "rm -f /system/xbin/su /.dockerenv", "pm uninstall --user 0 org.chromium.webview_shell"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少命令 %q：\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "mount -o bind") {
		t.Errorf("/.ppfake 不在说明重启前已挂完，不该再挂：\n%s", joined)
	}
	if st := svc.settings.get(id); st == nil || st.State == "pending" {
		t.Fatalf("设置应已做完：%+v", st)
	}
	if st := svc.fakes.get(id); st == nil || st.State != "applied" || len(st.Applied) != 2 {
		t.Fatalf("伪造文件状态应按 /proc/mounts 恢复成 applied（2 个）：%+v", st)
	}
	if svc.Busy() {
		t.Fatal("做完后不该 busy")
	}
}

func TestResumeFakesContinuesWithoutDoubleMount(t *testing.T) {
	svc, id := startForResume(t)
	// 重启前只挂好了 cpuinfo，/.ppfake 还在
	r := &resumeRunner{ppfake: true, mounts: []string{"/proc/cpuinfo"}}
	svc.run = r
	svc.ResumeRunning(context.Background())
	joined := strings.Join(r.calls, "\n")
	if strings.Contains(joined, "/cpuinfo /proc/cpuinfo") {
		t.Errorf("已挂上的 cpuinfo 不该重复挂：\n%s", joined)
	}
	if !strings.Contains(joined, "mount -o bind "+fakeMount+"/proc_version /proc/version") {
		t.Errorf("没挂的应接着挂：\n%s", joined)
	}
	if !strings.Contains(joined, "umount -l "+fakeMount) {
		t.Errorf("挂完要卸掉临时挂载点：\n%s", joined)
	}
	if st := svc.fakes.get(id); st == nil || st.State != "applied" {
		t.Fatalf("状态应为 applied：%+v", st)
	}
}

func TestResumeSkipsStoppedPhones(t *testing.T) {
	svc, id := startForResume(t)
	_ = svc.Stop(context.Background(), id)
	r := &resumeRunner{}
	svc.run = r
	if n := svc.ResumeRunning(context.Background()); n != 0 || len(r.calls) != 0 {
		t.Fatalf("关着的手机不该补做：n=%d calls=%v", n, r.calls)
	}
}

func TestBusyWhilePending(t *testing.T) {
	svc, id := startForResume(t)
	if svc.Busy() {
		t.Fatal("没有进行中的设置时不该 busy")
	}
	svc.settings.set(id, SettingsStatus{State: "pending"})
	if !svc.Busy() {
		t.Fatal("设置进行中应 busy")
	}
}

// gmsRunner 让 pm path com.google.android.gms 返回数据分区里的 GMS（以前开过谷歌服务留下的）。
type gmsRunner struct{ fakeRunner }

func (r *gmsRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if len(args) >= 5 && args[2] == "pm" && args[3] == "path" && args[4] == "com.google.android.gms" {
		return "package:/data/app/~~x==/com.google.android.gms-y==/base.apk", nil
	}
	return r.fakeRunner.Run(ctx, name, args...)
}

func TestWarnLeftoverGMSWithoutGapps(t *testing.T) {
	svc, id := startForResume(t)
	svc.run = &gmsRunner{}
	svc.applySettings(context.Background(), id)
	if st := svc.settings.get(id); st == nil || !strings.Contains(st.Error, "GMS") {
		t.Fatalf("没开谷歌服务但留着 GMS 应提示：%+v", st)
	}
}

// 电量没填 / 填了 0 时，也必须先设成正常电量再设 present=1，否则安卓当成没电关机（踩坑 #47）。
func TestBatteryLevelSetBeforePresent(t *testing.T) {
	for _, lv := range []any{nil, float64(0)} {
		svc, id := startForResume(t)
		r := &fakeRunner{}
		svc.run = r
		v := map[string]any{"batteryCharging": "none"}
		if lv != nil {
			v["batteryLevel"] = lv
		}
		svc.applyBattery(context.Background(), id, containerName(id), v)
		levelAt, presentAt := -1, -1
		for i, c := range r.calls {
			if strings.HasSuffix(c, "dumpsys battery set level 50") || strings.HasSuffix(c, "dumpsys battery set level 1") {
				levelAt = i
			}
			if strings.HasSuffix(c, "dumpsys battery set present 1") {
				presentAt = i
			}
		}
		if levelAt < 0 || presentAt < 0 || levelAt > presentAt {
			t.Fatalf("电量 %v：要先设电量（>0）再设 present：\n%s", lv, strings.Join(r.calls, "\n"))
		}
	}
}
