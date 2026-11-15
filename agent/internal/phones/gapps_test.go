package phones

import (
	"context"
	"errors"
	"testing"
)

// 带谷歌服务的手机：没装谷歌服务镜像时禁止创建；装了就用谷歌服务镜像开机，普通手机仍用干净 AOSP 镜像。
func TestGappsImageSelection(t *testing.T) {
	svc, fd, _ := newTestService(t, true, "software")

	// 没配谷歌服务镜像 → 建带谷歌服务的手机应被拒绝
	_, err := svc.Create(CreateRequest{Name: "g", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID, Gapps: true})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("没谷歌服务镜像应拒绝创建带谷歌服务的手机，实际 err=%v", err)
	}

	// 配上谷歌服务镜像
	svc.cfg.GappsImages = map[int]string{14: "redroid:14-gapps"}
	p, err := svc.Create(CreateRequest{Name: "g", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID, Gapps: true})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Gapps {
		t.Fatal("创建后应记住 Gapps=true")
	}
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if got := fd.created["pp-"+p.ID].Image; got != "redroid:14-gapps" {
		t.Fatalf("带谷歌服务应该用谷歌服务镜像，实际 %q", got)
	}

	// 普通手机仍用干净 AOSP 镜像
	p2, err := svc.Create(CreateRequest{Name: "n", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(context.Background(), p2.ID); err != nil {
		t.Fatal(err)
	}
	if got := fd.created["pp-"+p2.ID].Image; got != "redroid:14" {
		t.Fatalf("普通手机应该用干净镜像，实际 %q", got)
	}
}
