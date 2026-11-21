package phones

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 踩坑 #122：没有 systemd（Alpine 板子）时用 OpenRC 的 supervise-daemon 跑 rootcanal。
func TestBTOpenRCLauncher(t *testing.T) {
	oldLook, oldBin := lookPath, RootcanalBin
	t.Cleanup(func() { lookPath, RootcanalBin = oldLook, oldBin })
	RootcanalBin = filepath.Join(t.TempDir(), "pp-rootcanal")
	_ = os.WriteFile(RootcanalBin, []byte("#!/bin/sh\n"), 0o755)

	lookPath = func(f string) (string, error) {
		if f == "supervise-daemon" {
			return "/sbin/supervise-daemon", nil
		}
		return "", errors.New("no")
	}
	if btLauncher() != "openrc" || !btAvailable() {
		t.Fatalf("没有 systemd 有 supervise-daemon 应选 openrc：%q", btLauncher())
	}
	svc, _, _ := newTestService(t, true, "")
	svc.stopBT(context.Background(), "abc")
	calls := strings.Join(svc.run.(*fakeRunner).calls, "\n")
	if !strings.Contains(calls, "env RC_SVCNAME=ppbt-abc supervise-daemon ppbt-abc --stop --pidfile /run/ppbt-abc.pid") || strings.Contains(calls, "systemctl") {
		t.Fatalf("openrc 下应调 supervise-daemon 停：\n%s", calls)
	}

	lookPath = func(string) (string, error) { return "", errors.New("no") }
	if btAvailable() {
		t.Fatal("两种守护工具都没有时不该认为蓝牙可用")
	}
	lookPath = func(f string) (string, error) { return "/usr/bin/" + f, nil }
	if btLauncher() != "systemd" {
		t.Fatal("有 systemd-run 时优先 systemd")
	}
}
