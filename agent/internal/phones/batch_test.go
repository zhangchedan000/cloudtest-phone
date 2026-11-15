package phones

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBatchInstallAndUpload(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	ctx := context.Background()
	var ids []string
	for _, n := range []string{"A", "B", "C"} {
		p, err := svc.Create(CreateRequest{Name: n, ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	// A、B 开机，C 关着（MaxRunning=2）
	for _, id := range ids[:2] {
		if err := svc.Start(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	r := &pmRunner{}
	svc.run = r

	res, err := svc.InstallAPKMany(ctx, ids, "demo.apk", strings.NewReader("apk-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || !res[0].OK || !res[1].OK || res[2].OK || res[0].ID != ids[0] || res[2].ID != ids[2] {
		t.Fatalf("开着的装上、关着的报错，按顺序返回：%+v", res)
	}
	if !strings.Contains(res[2].Error, "没有开机") && !errors.Is(errors.New(res[2].Error), ErrNotRunning) && res[2].Error == "" {
		t.Fatalf("关机的应说明原因：%+v", res[2])
	}
	// 文件只存了一份（每台各 cp 一次）
	cps := 0
	for _, c := range r.calls {
		if strings.HasPrefix(c, "docker cp ") {
			cps++
		}
	}
	if cps != 2 {
		t.Fatalf("应该给两台开着的各 cp 一次：%d", cps)
	}

	res, err = svc.UploadFileMany(ctx, ids[:2], "DCIM", "视频.mp4", strings.NewReader("video"))
	if err != nil || len(res) != 2 || !res[0].OK || res[0].Path != "/sdcard/DCIM/Camera/视频.mp4" {
		t.Fatalf("批量传文件：%+v %v", res, err)
	}

	for _, bad := range []func() error{
		func() error { _, err := svc.InstallAPKMany(ctx, nil, "a.apk", strings.NewReader("x")); return err },
		func() error { _, err := svc.InstallAPKMany(ctx, ids, "a.zip", strings.NewReader("x")); return err },
		func() error {
			_, err := svc.UploadFileMany(ctx, ids, "/system", "a", strings.NewReader("x"))
			return err
		},
	} {
		if err := bad(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("应拒绝：%v", err)
		}
	}
}
