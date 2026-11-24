package phones

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mountRunner 模拟宿主机的 mountpoint / mount / umount / mkfs.ext4，其他命令交给 fakeRunner。
type mountRunner struct {
	fakeRunner
	mounted map[string]bool
}

func (r *mountRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	switch name {
	case "mountpoint":
		r.calls = append(r.calls, name+" "+strings.Join(args, " "))
		if r.mounted[args[len(args)-1]] {
			return "", nil
		}
		return "", errors.New("not a mountpoint")
	case "mount":
		r.calls = append(r.calls, name+" "+strings.Join(args, " "))
		r.mounted[args[len(args)-1]] = true
		return "", nil
	case "umount":
		r.calls = append(r.calls, name+" "+strings.Join(args, " "))
		delete(r.mounted, args[len(args)-1])
		return "", nil
	case "mkfs.ext4":
		r.calls = append(r.calls, name+" "+strings.Join(args, " "))
		return "", nil
	}
	return r.fakeRunner.Run(ctx, name, args...)
}

func storageService(t *testing.T) (*Service, *mountRunner) {
	svc, _, _ := newTestService(t, true, "software")
	svc.cfg.DataImages = true
	r := &mountRunner{mounted: map[string]bool{}}
	svc.run = r
	return svc, r
}

func TestNewPhoneGetsSizedDataImage(t *testing.T) {
	svc, r := storageService(t)
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(svc.dataImage(p.ID))
	if err != nil {
		t.Fatalf("新手机应建存储镜像：%v", err)
	}
	// POCO X4 GT 档案 storageTotal（GB，十进制）的 94%（真机 /data 比标称小一截），再加 ext4 元数据的 2.15%
	if want := storageBytes(map[string]any{"storageTotal": svc.model("poco-x4-gt").Values["storageTotal"]}, svc.model("poco-x4-gt")); want == 0 || info.Size() != want+want*215/10000 {
		t.Fatalf("镜像大小不对：%d", info.Size())
	}
	joined := strings.Join(r.calls, "\n")
	if !strings.Contains(joined, "mkfs.ext4") || !r.mounted[svc.dataDir(p.ID)] {
		t.Fatalf("应格式化并挂载：\n%s", joined)
	}
	// 再开一次机：已经挂着，不重复建 / 挂
	n := strings.Count(joined, "mkfs.ext4")
	_ = svc.Start(context.Background(), p.ID)
	if strings.Count(strings.Join(r.calls, "\n"), "mkfs.ext4") != n {
		t.Fatal("不该重复格式化")
	}
}

func TestOldPhoneWithDataKeepsDirectory(t *testing.T) {
	svc, r := storageService(t)
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	// 老手机：数据目录里已经有东西、没有镜像
	_ = os.MkdirAll(filepath.Join(svc.dataDir(p.ID), "app"), 0o755)
	if err := svc.Start(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(svc.dataImage(p.ID)); err == nil {
		t.Fatal("老手机不该建镜像（不迁移数据）")
	}
	if strings.Contains(strings.Join(r.calls, "\n"), "mount -o loop") {
		t.Fatal("老手机不该挂镜像")
	}
}

func TestResetMovesImageToTrash(t *testing.T) {
	svc, r := storageService(t)
	ctx := context.Background()
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	_ = svc.Start(ctx, p.ID)
	if err := svc.Reset(ctx, p.ID, ResetRequest{}); err != nil {
		t.Fatal(err)
	}
	// 重置时卸掉旧镜像、挪进回收站；原来开着的会自动重新开机，按机型容量建一个新的空镜像
	if !strings.Contains(strings.Join(r.calls, "\n"), "umount "+svc.dataDir(p.ID)) {
		t.Fatal("重置前要卸掉镜像")
	}
	if _, err := os.Stat(svc.dataImage(p.ID)); err != nil || !r.mounted[svc.dataDir(p.ID)] {
		t.Fatal("重新开机后应有新的空镜像并挂上")
	}
	items, _ := svc.ListTrash()
	if len(items) != 1 || items[0].Kind != "reset" {
		t.Fatalf("回收站应有一份重置的旧数据：%+v", items)
	}
	if _, err := os.Stat(filepath.Join(svc.trashDir(), items[0].Name, "data.img")); err != nil {
		t.Fatalf("回收站里应有 data.img：%v", err)
	}
	if items[0].SizeMB > 100 {
		t.Fatalf("稀疏镜像应按实际占用算大小，不是表面大小：%.0f MB", items[0].SizeMB)
	}
}

func TestStorageBytesUsesRealDataSize(t *testing.T) {
	m := &Model{Values: map[string]any{"storageTotal": 256}, DataBytes: 241380061184}
	if got := storageBytes(map[string]any{"storageTotal": 256}, m); got != 241380061184 {
		t.Fatalf("标称容量和机型一样时用真机 /data 字节数：%d", got)
	}
	if got := storageBytes(map[string]any{"storageTotal": 512}, m); got != 512*940_000_000 {
		t.Fatalf("手动改了容量就按 94%%：%d", got)
	}
	if got := storageBytes(map[string]any{}, m); got != 0 {
		t.Fatalf("没填容量返回 0：%d", got)
	}
}
