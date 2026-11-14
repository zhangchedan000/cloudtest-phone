package phones

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/zhangchedan000/prism-phone/agent/internal/docker"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

type fakeDocker struct {
	created map[string]docker.ContainerSpec
	running map[string]bool
}

func newFake() *fakeDocker {
	return &fakeDocker{created: map[string]docker.ContainerSpec{}, running: map[string]bool{}}
}

func (f *fakeDocker) CreateContainer(_ context.Context, spec docker.ContainerSpec) (string, error) {
	if _, ok := f.created[spec.Name]; ok {
		return "", &docker.APIError{Status: 409, Message: "已存在"}
	}
	f.created[spec.Name] = spec
	return spec.Name, nil
}
func (f *fakeDocker) StartContainer(_ context.Context, name string) error { f.running[name] = true; return nil }
func (f *fakeDocker) StopContainer(_ context.Context, name string, _ int) error {
	f.running[name] = false
	return nil
}
func (f *fakeDocker) RemoveContainer(_ context.Context, name string) error {
	delete(f.created, name)
	delete(f.running, name)
	return nil
}
func (f *fakeDocker) InspectContainer(_ context.Context, name string) (*docker.ContainerInfo, error) {
	if _, ok := f.created[name]; !ok {
		return nil, &docker.APIError{Status: 404}
	}
	info := &docker.ContainerInfo{}
	info.State.Running = f.running[name]
	return info, nil
}
func (f *fakeDocker) EnsureNetwork(context.Context, string, string, string) error { return nil }

// 用仓库里真实的 profiles/（和管理页同一份）。
func loadRealProfiles(t *testing.T) (*Catalog, []Model) {
	t.Helper()
	cat, models, err := LoadProfiles(os.DirFS("../../.."), "profiles")
	if err != nil {
		t.Fatal(err)
	}
	return cat, models
}

func newTestService(t *testing.T, dev bool, gpu string) (*Service, *fakeDocker, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cat, models := loadRealProfiles(t)
	fd := newFake()
	cfg := Config{HostID: "h1", GPUFamily: gpu, GPUMode: "guest", Images: map[int]string{14: "redroid:14"}, DataDir: dir, Network: "pp-net", Subnet: "10.88.0.0/16", MemoryMB: 3072, CPUs: 2, MaxRunning: 2, DevHost: dev}
	return NewService(cfg, fd, st, cat, models), fd, st
}

func addProxy(t *testing.T, st *store.Store, id string, ok bool) {
	t.Helper()
	err := st.Update(func(s *store.State) error {
		s.Proxies = append(s.Proxies, store.Proxy{ID: id, Name: id, Protocol: "socks5", Address: "socks5://x", Check: &store.ProxyCheck{OK: ok, Country: "US", Timezone: "America/Chicago"}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateGivesUniqueIDsAndIPs(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	a, err := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Create(CreateRequest{Name: "B", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err != nil {
		t.Fatal(err)
	}
	if a.IP == b.IP || a.IP != "10.88.1.2" || b.IP != "10.88.1.3" {
		t.Fatalf("IP 分配不对：%s %s", a.IP, b.IP)
	}
	if a.Fields["androidId"].Value == b.Fields["androidId"].Value {
		t.Fatal("两台的 Android ID 不能一样")
	}
	if a.Fields["timezone"].Mode != "proxy" {
		t.Fatalf("时区默认应跟随代理，实际 %s", a.Fields["timezone"].Mode)
	}
}

func TestCreateRejectsUnknownModelAndVersion(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	if _, err := svc.Create(CreateRequest{Name: "A", ModelID: "nope", AndroidVersion: 14}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("应该拒绝未知机型：%v", err)
	}
	if _, err := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 9}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("应该拒绝没有镜像的版本：%v", err)
	}
}

func TestStartBlockedWithoutProxy(t *testing.T) {
	svc, fd, _ := newTestService(t, false, "Mali-G610")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14})
	if err := svc.Start(context.Background(), p.ID); !errors.Is(err, ErrBlocked) {
		t.Fatalf("没有代理必须禁止开机：%v", err)
	}
	if len(fd.created) != 0 {
		t.Fatal("被禁止时不能创建容器")
	}
}

func TestDirectProxyOnlyOnDevHost(t *testing.T) {
	svc, _, _ := newTestService(t, false, "Mali-G610")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err := svc.Start(context.Background(), p.ID); !errors.Is(err, ErrBlocked) {
		t.Fatalf("正式机器不能用直连：%v", err)
	}
}

func TestStartBlockedWhenProxyDown(t *testing.T) {
	svc, _, st := newTestService(t, false, "Mali-G610")
	addProxy(t, st, "bad", false)
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: "bad"})
	if err := svc.Start(context.Background(), p.ID); !errors.Is(err, ErrBlocked) {
		t.Fatalf("代理不通必须禁止开机：%v", err)
	}
}

func TestGPUMismatchBlocks(t *testing.T) {
	svc, _, st := newTestService(t, false, "Adreno-830")
	addProxy(t, st, "ok", true)
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: "ok"})
	if err := svc.Start(context.Background(), p.ID); !errors.Is(err, ErrBlocked) || !strings.Contains(err.Error(), "显卡") {
		t.Fatalf("显卡对不上必须禁止开机：%v", err)
	}
}

func TestStartCreatesContainerWithProps(t *testing.T) {
	svc, fd, st := newTestService(t, false, "Mali-G610")
	addProxy(t, st, "ok", true)
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: "ok"})
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	spec, ok := fd.created["pp-"+p.ID]
	if !ok {
		t.Fatal("没有创建容器")
	}
	cmd := strings.Join(spec.Cmd, " ")
	for _, want := range []string{"ro.product.model=22041216G", "ro.product.brand=POCO", "androidboot.redroid_width=1080", "androidboot.redroid_dpi=440", "ro.serialno="} {
		if !strings.Contains(cmd, want) {
			t.Errorf("启动参数缺少 %q：%s", want, cmd)
		}
	}
	// 带空格的值（POCO X4 GT）不能传：实测会被截断
	if strings.Contains(cmd, "ro.product.marketname") {
		t.Errorf("带空格的市场名不该出现在启动参数里：%s", cmd)
	}
	// persist.* 用启动参数设不进去，不传
	if strings.Contains(cmd, "persist.sys") {
		t.Errorf("persist 属性不该在启动参数里：%s", cmd)
	}
	if spec.IPv4 != p.IP || spec.MemoryMB != 3072 || !spec.Privileged {
		t.Errorf("容器参数不对：%+v", spec)
	}
	if spec.Binds[0] != filepath.Join(svc.cfg.DataDir, "phones", p.ID, "data")+":/data" {
		t.Errorf("数据目录挂载不对：%v", spec.Binds)
	}
}

func TestMaxRunning(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	ctx := context.Background()
	var ids []string
	for _, n := range []string{"A", "B", "C"} {
		p, _ := svc.Create(CreateRequest{Name: n, ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
		ids = append(ids, p.ID)
	}
	if err := svc.Start(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, ids[2]); !errors.Is(err, ErrBlocked) {
		t.Fatalf("超过同时运行上限要拒绝：%v", err)
	}
}

func TestSaveValidatesModes(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err := svc.Save(p.ID, SaveRequest{Fields: map[string]store.FieldSetting{"brand": {Mode: "random"}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("品牌不支持随机模式，应该拒绝：%v", err)
	}
	if err := svc.Save(p.ID, SaveRequest{Fields: map[string]store.FieldSetting{"brand": {Mode: "manual", Value: "Xiaomi"}}}); err != nil {
		t.Fatal(err)
	}
	if got := svc.List(context.Background())[0].Fields["brand"]; got.Value != "Xiaomi" {
		t.Fatalf("保存后读不到：%+v", got)
	}
}

func TestRenewKeepsLockedFields(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	locked := p.Fields["serial"]
	locked.Locked = true
	_ = svc.Save(p.ID, SaveRequest{Fields: map[string]store.FieldSetting{"serial": locked}})
	if err := svc.Renew(p.ID); err != nil {
		t.Fatal(err)
	}
	after := svc.List(context.Background())[0]
	if after.Fields["serial"].Value != locked.Value {
		t.Fatal("锁定的字段不能被一键新机改掉")
	}
	if after.Fields["androidId"].Value == p.Fields["androidId"].Value {
		t.Fatal("没锁定的随机字段应该变")
	}
}

func TestDeleteMovesDataToTrash(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	_ = svc.Start(context.Background(), p.ID)
	if err := svc.Delete(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(svc.cfg.DataDir, "trash"))
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), p.ID) {
		t.Fatalf("数据应该挪到回收站：%v", entries)
	}
	if len(svc.List(context.Background())) != 0 {
		t.Fatal("删除后列表应该为空")
	}
}

func TestProfilesLoadFromSameFilesAsWeb(t *testing.T) {
	cat, models := loadRealProfiles(t)
	if len(cat.Fields) < 30 || len(models) < 3 {
		t.Fatalf("字段 %d 个、机型 %d 个，太少", len(cat.Fields), len(models))
	}
	_ = fstest.MapFS{}
}
