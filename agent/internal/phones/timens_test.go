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
	// 多开 binder：第一台拿 slot 0 = binder 1,2,3，pp-init 在容器里绑成标准名。
	want := []string{"/data/.ppi", "pp-init", "--binder=1,2,3", "0", "/init", "androidboot.hardware=redroid"}
	if !reflect.DeepEqual(spec.Entrypoint, want) {
		t.Fatalf("入口不对：%v", spec.Entrypoint)
	}
	if spec.Labels["prism-phone.binder"] != "0" {
		t.Fatalf("binder 槽位标签不对：%v", spec.Labels)
	}
	if !strings.Contains(strings.Join(spec.Binds, " "), "/usr/local/bin/prism-phone-agent:/data/.ppi:ro") {
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

func TestWithBinder(t *testing.T) {
	ep := []string{"/x", "pp-init", "0", "/init", "arg"}
	got := withBinder(ep, "4,5,6")
	want := []string{"/x", "pp-init", "--binder=4,5,6", "0", "/init", "arg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withBinder 插错位置：%v", got)
	}
	// 空参数 / 非 pp-init 入口：原样返回
	if got := withBinder(ep, ""); !reflect.DeepEqual(got, ep) {
		t.Fatalf("空参数应原样返回：%v", got)
	}
	if got := withBinder([]string{"/init"}, "1,2,3"); !reflect.DeepEqual(got, []string{"/init"}) {
		t.Fatalf("非 pp-init 入口应原样返回：%v", got)
	}
	if withBinder(nil, "1,2,3") != nil {
		t.Fatal("nil 入口应返回 nil")
	}
}

func TestPhoneCommandFallsBackToDockerExec(t *testing.T) {
	svc, id := startForResume(t)
	// 没开时间命名空间：docker exec
	if c := svc.phoneCommand(context.Background(), id, "screenrecord"); !strings.HasSuffix(c.Path, "docker") || c.Args[1] != "exec" {
		t.Fatalf("应该用 docker exec：%v", c.Args)
	}
	// 开了，但拿不到容器进程号（假 Docker 的 Pid 是 0）：也退回 docker exec，不会拼出坏命令
	svc.cfg.InitHelper = "/x"
	if c := svc.phoneCommand(context.Background(), id, "screenrecord"); c.Args[1] != "exec" {
		t.Fatalf("拿不到 Pid 时应退回 docker exec：%v", c.Args)
	}
}
