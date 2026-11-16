package phones

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAdbOffByDefaultAndToggle(t *testing.T) {
	svc, id := startForResume(t)
	r := &fakeRunner{}
	svc.run = r
	svc.applySettings(context.Background(), id)
	if j := strings.Join(r.calls, "\n"); !strings.Contains(j, "settings put global adb_enabled 0") || !strings.Contains(j, "setprop persist.sys.usb.config mtp") || !strings.Contains(j, "setprop sys.usb.state mtp") {
		t.Fatalf("默认应关掉 USB 调试：\n%s", strings.Join(r.calls, "\n"))
	}
	on := true
	r.calls = nil
	if err := svc.Save(id, SaveRequest{Adb: &on}); err != nil {
		t.Fatal(err)
	}
	st := svc.store.Snapshot()
	if p, _ := st.Phone(id); !p.Adb {
		t.Fatal("开关没存上")
	}
	// 开着的手机：保存后马上（后台）打开
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(strings.Join(r.calls, "\n"), "settings put global adb_enabled 1") || !strings.Contains(strings.Join(r.calls, "\n"), "setprop persist.sys.usb.config mtp,adb") {
		if time.Now().After(deadline) {
			t.Fatalf("打开开关后应马上设 adb_enabled=1：\n%s", strings.Join(r.calls, "\n"))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
