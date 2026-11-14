package phones

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSafeFileName(t *testing.T) {
	for in, want := range map[string]string{
		"视频 01.mp4":         "视频 01.mp4",
		"../../etc/passwd":  "passwd",
		`C:\x\a;rm -rf.jpg`: "a_rm -rf.jpg",
		".hidden":           "hidden",
	} {
		got, err := safeFileName(in)
		if err != nil || got != want {
			t.Fatalf("%q → %q %v，想要 %q", in, got, err, want)
		}
	}
	if _, err := safeFileName("///"); !errors.Is(err, ErrInvalid) {
		t.Fatal("空文件名应拒绝")
	}
}

// pmRunner 让 pm install 返回 Success。
type pmRunner struct{ fakeRunner }

func (r *pmRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := r.fakeRunner.Run(ctx, name, args...)
	if len(args) >= 3 && args[2] == "pm" {
		return "Success", nil
	}
	return out, err
}

func TestInstallAndUpload(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	ctx := context.Background()
	if _, err := svc.InstallAPK(ctx, p.ID, "a.apk", strings.NewReader("x")); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("关机时应拒绝：%v", err)
	}
	if err := svc.Start(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	r := &pmRunner{}
	svc.run = r
	if _, err := svc.InstallAPK(ctx, p.ID, "tiktok.APK", strings.NewReader("apk-bytes")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.InstallAPK(ctx, p.ID, "a.zip", strings.NewReader("x")); !errors.Is(err, ErrInvalid) {
		t.Fatal("不是 apk 应拒绝")
	}
	remote, err := svc.UploadFile(ctx, p.ID, "DCIM", "../视频.mp4", strings.NewReader("video"))
	if err != nil || remote != "/sdcard/DCIM/Camera/视频.mp4" {
		t.Fatalf("上传路径不对：%q %v", remote, err)
	}
	if _, err := svc.UploadFile(ctx, p.ID, "/system", "a", strings.NewReader("x")); !errors.Is(err, ErrInvalid) {
		t.Fatal("不允许的目录应拒绝")
	}
	if _, err := svc.UploadFile(ctx, p.ID, "Download", "empty.txt", strings.NewReader("")); !errors.Is(err, ErrInvalid) {
		t.Fatal("空文件应拒绝")
	}
	calls := strings.Join(r.calls, "\n")
	for _, want := range []string{"pm install -r /data/local/tmp/pp-install.apk", ":/sdcard/DCIM/Camera/视频.mp4", "MEDIA_SCANNER_SCAN_FILE"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("缺少命令 %q：\n%s", want, calls)
		}
	}
}
