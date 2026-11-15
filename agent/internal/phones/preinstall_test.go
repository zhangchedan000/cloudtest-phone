package phones

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// markerRunner 假装手机：pm install 成功，记录文件存在内存里（echo >> 追加，cat 读出）。
type markerRunner struct {
	fakeRunner
	marker    string
	installs  int
	relayouts int
	fail      bool
}

func (r *markerRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	_, _ = r.fakeRunner.Run(ctx, name, args...)
	if len(args) >= 4 && args[2] == "cat" && args[3] == preinstallMarker {
		if r.marker == "" { // 和真机一样：文件不存在时报错，输出里是报错文字
			return "cat: " + preinstallMarker + ": No such file or directory", errors.New("exit status 1")
		}
		return r.marker, nil
	}
	if len(args) >= 4 && args[2] == "cmd" && args[3] == "package" {
		return "priority=0 preferredOrder=0\ncom.android.launcher3/.uioverrides.QuickstepLauncher", nil
	}
	if len(args) >= 4 && args[2] == "pm" && args[3] == "clear" {
		r.relayouts++
		return "Success", nil
	}
	if len(args) >= 3 && args[2] == "pm" {
		r.installs++
		if r.fail {
			return "Failure [INSTALL_FAILED_INVALID_APK]", nil
		}
		return "Success", nil
	}
	if len(args) >= 5 && args[2] == "sh" && strings.HasPrefix(args[4], "echo ") {
		line := strings.TrimSuffix(strings.TrimPrefix(args[4], "echo "), " >> "+preinstallMarker)
		r.marker += strings.Trim(line, "'") + "\n"
	}
	return "", nil
}

func TestPreinstall(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	ctx := context.Background()
	r := &markerRunner{}
	svc.run = r

	// 没有预装目录：什么都不做
	if inst, probs := svc.preinstall(ctx, "a"); len(inst) != 0 || len(probs) != 0 {
		t.Fatalf("没有目录应该什么都不做：%v %v", inst, probs)
	}
	dir := svc.preinstallDir()
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "chrome.apk"), []byte("chrome"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b app.APK"), []byte("bb"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "说明.txt"), []byte("不是 apk"), 0o644)

	inst, probs := svc.preinstall(ctx, "a")
	if len(inst) != 2 || inst[0] != "b app.APK" || inst[1] != "chrome.apk" || len(probs) != 0 {
		t.Fatalf("第一次开机应装两个：%v %v", inst, probs)
	}
	if r.relayouts != 1 {
		t.Fatalf("全新手机装完应让桌面重新布局一次（%d）", r.relayouts)
	}
	// 第二次开机：都装过了
	if inst, _ := svc.preinstall(ctx, "a"); len(inst) != 0 || r.installs != 2 {
		t.Fatalf("装过的不应再装：%v（pm 调用 %d 次）", inst, r.installs)
	}
	// 换了新版本（大小变了）：装新版本
	_ = os.WriteFile(filepath.Join(dir, "chrome.apk"), []byte("chrome-v2"), 0o644)
	if inst, _ := svc.preinstall(ctx, "a"); len(inst) != 1 || inst[0] != "chrome.apk" {
		t.Fatalf("新版本应该装：%v", inst)
	}
	if r.relayouts != 1 {
		t.Fatal("只是升级版本，不该动用户的桌面")
	}
	// 重置后（记录没了）：全部重装，桌面重新布局
	r.marker = ""
	if inst, _ := svc.preinstall(ctx, "a"); len(inst) != 2 || r.relayouts != 2 {
		t.Fatalf("重置后应全部重装并重新布局：%v（布局 %d 次）", inst, r.relayouts)
	}
	// 安装失败：报问题，不记录（下次开机再试）
	r.marker, r.fail = "", true
	inst, probs = svc.preinstall(ctx, "a")
	if len(inst) != 0 || len(probs) != 2 || r.marker != "" {
		t.Fatalf("失败应报问题且不记录：%v %v %q", inst, probs, r.marker)
	}
}
