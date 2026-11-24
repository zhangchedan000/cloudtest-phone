package phones

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

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
func (f *fakeDocker) StartContainer(_ context.Context, name string) error {
	f.running[name] = true
	return nil
}
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
	cfg := Config{HostID: "h1", GPUFamily: gpu, GPUMode: "guest", Images: map[int]string{14: "redroid:14"}, DataDir: dir, Network: "pp-net", Subnet: "10.88.0.0/16", MemoryMB: 3072, CPUs: 2, MaxRunning: 2, DevHost: dev, PollEvery: time.Millisecond}
	svc := NewService(cfg, fd, st, cat, models)
	svc.run = &fakeRunner{}
	svc.async = false
	return svc, fd, st
}

// fakeRunner 假装执行 docker exec：getprop 返回已开机，其他命令都成功，记下调用。
type fakeRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	r.mu.Unlock()
	if len(args) >= 3 && args[2] == "getprop" {
		return "1", nil
	}
	if len(args) >= 5 && args[2] == "sh" && args[4] == readyProbe {
		return "1\nService input: found", nil
	}
	return "", nil
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
	// ro.hardware 改了会开不了机（实测），不能传（硬件名走入口的 androidboot.hardware，#149）；
	// ro.board.platform 现在按机型传（踩坑 #153，硬件名单独处理以后平台名只是兜底）
	if strings.Contains(cmd, " ro.hardware=") {
		t.Errorf("ro.hardware 不能出现在启动参数里：%s", cmd)
	}
	if !strings.Contains(cmd, "ro.board.platform=mt6895") {
		t.Errorf("平台名应按机型传：%s", cmd)
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

func TestStartMountsFakeFiles(t *testing.T) {
	svc, fd, _ := newTestService(t, true, "software")
	svc.cfg.HostCPUs = 4
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	spec := fd.created["pp-"+p.ID]
	// binds：/data、3 条多开 binder、伪造目录。用 Contains 判断（不写死顺序/数量）。
	if !strings.Contains(strings.Join(spec.Binds, " "), ":"+fakeMount+":ro") {
		t.Fatalf("伪造目录没挂进容器：%v", spec.Binds)
	}
	cpuinfo, err := os.ReadFile(filepath.Join(svc.fakeDir(p.ID), "cpuinfo"))
	if err != nil {
		t.Fatal(err)
	}
	// 宿主机只有 4 核：只写 4 个核（按比例 2 个 A55 + 2 个 A78），不能写出 8 个
	if n := strings.Count(string(cpuinfo), "processor\t:"); n != 4 {
		t.Fatalf("应该写 4 个核，实际 %d：\n%s", n, cpuinfo)
	}
	if !strings.Contains(string(cpuinfo), "CPU part\t: 0xd05") {
		t.Fatalf("CPU 型号不对：\n%s", cpuinfo)
	}
	ver, _ := os.ReadFile(filepath.Join(svc.fakeDir(p.ID), "proc_version"))
	if !strings.HasPrefix(string(ver), "Linux version 5.10.") {
		t.Fatalf("/proc/version 应该是 5.10（POCO X4 GT 出厂 Android 12）：%s", ver)
	}
	r := svc.run.(*fakeRunner)
	joined := strings.Join(r.calls, "\n")
	for _, want := range []string{"mount -o bind " + fakeMount + "/cpuinfo /proc/cpuinfo", "mount -o bind " + fakeMount + "/proc_version /proc/version", "umount -l " + fakeMount, "rmdir " + fakeMount} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少命令 %q：\n%s", want, joined)
		}
	}
	if st := svc.fakes.get(p.ID); st == nil || st.State != "applied" {
		t.Fatalf("挂载状态不对：%+v", st)
	}
}

func TestResetClearsDataKeepsIdentity(t *testing.T) {
	svc, fd, _ := newTestService(t, true, "software")
	ctx := context.Background()
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	_ = svc.Start(ctx, p.ID)
	marker := filepath.Join(svc.cfg.DataDir, "phones", p.ID, "data", "app.db")
	_ = os.WriteFile(marker, []byte("x"), 0o644)
	if err := svc.Reset(ctx, p.ID, ResetRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("重置后旧数据应该不在了")
	}
	trash, _ := os.ReadDir(filepath.Join(svc.cfg.DataDir, "trash"))
	if len(trash) != 1 || !strings.Contains(trash[0].Name(), "-reset-") {
		t.Fatalf("旧数据应该进回收站：%v", trash)
	}
	after := svc.List(ctx)[0]
	if after.Fields["androidId"].Value != p.Fields["androidId"].Value {
		t.Fatal("不选「换新机」时，参数不能变")
	}
	if !fd.running["pp-"+p.ID] {
		t.Fatal("原来在运行的，重置后要重新开机")
	}
}

func TestResetRenewAndReflash(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	svc.cfg.Images[13] = "redroid:13"
	ctx := context.Background()
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err := svc.Reset(ctx, p.ID, ResetRequest{Renew: true, AndroidVersion: 13}); err != nil {
		t.Fatal(err)
	}
	after := svc.List(ctx)[0]
	if after.AndroidVersion != 13 {
		t.Fatalf("刷机后应该是 Android 13，实际 %d", after.AndroidVersion)
	}
	if after.Fields["androidId"].Value == p.Fields["androidId"].Value {
		t.Fatal("选了换新机，Android ID 应该变")
	}
	if err := svc.Reset(ctx, p.ID, ResetRequest{AndroidVersion: 9}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("没有镜像的版本应该拒绝：%v", err)
	}
}
