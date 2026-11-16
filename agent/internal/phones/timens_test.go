package phones

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// epDocker 假 Docker 加上「读镜像入口」。
type epDocker struct{ *fakeDocker }

func (epDocker) ImageEntrypoint(context.Context, string) ([]string, error) {
	return []string{"/init", "androidboot.hardware=redroid"}, nil
}

func TestStartWrapsEntrypointWithInitHelper(t *testing.T) {
	svc, fd, _ := newTestService(t, true, "software")
	svc.docker = epDocker{fd}
	svc.cfg.InitHelper = "/usr/local/bin/prism-phone-agent"
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	spec := fd.created["pp-"+p.ID]
	want := []string{"/pp-init", "pp-init", "0", "/init", "androidboot.hardware=redroid"}
	if !reflect.DeepEqual(spec.Entrypoint, want) {
		t.Fatalf("入口不对：%v", spec.Entrypoint)
	}
	if !strings.Contains(strings.Join(spec.Binds, " "), "/usr/local/bin/prism-phone-agent:/pp-init:ro") {
		t.Fatalf("启动器没挂进去：%v", spec.Binds)
	}
}

func TestNoInitHelperKeepsImageEntrypoint(t *testing.T) {
	svc, fd, _ := newTestService(t, true, "software")
	svc.docker = epDocker{fd}
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	_ = svc.Start(context.Background(), p.ID)
	if spec := fd.created["pp-"+p.ID]; spec.Entrypoint != nil {
		t.Fatalf("没配启动器时不该改入口：%v", spec.Entrypoint)
	}
}
