package wifi

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// phyRun 假的命令：phy0、phy1 已被占用
type phyRun struct{ calls []string }

func (f *phyRun) Run(_ context.Context, name string, args ...string) (string, error) {
	c := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, c)
	switch {
	case strings.Contains(c, "dev wlan0 info"):
		return "Interface wlan0\n\tifindex 3\n\twdev 0x100000001\n\taddr 02:11:22:33:44:55\n\ttype managed\n\twiphy 7\n", nil
	case strings.Contains(c, "set name phy0"), strings.Contains(c, "set name phy1"):
		return "command failed: File exists (-17)", errors.New("exit 1")
	}
	return "", nil
}

func TestRenamePhy(t *testing.T) {
	r := &phyRun{}
	if got := renamePhy(context.Background(), r, "1234"); got != "phy2" {
		t.Fatalf("应该改成第一个空着的 phy2，得到 %q（%v）", got, r.calls)
	}
	if !strings.Contains(strings.Join(r.calls, "\n"), "nsenter -t 1234 -n iw phy#7 set name phy2") {
		t.Fatalf("要按 wiphy 编号改：%v", r.calls)
	}
}

func TestRestorePhyName(t *testing.T) {
	r := &phyRun{}
	RestorePhyName(context.Background(), r, "1234", "pps4e0d5145c9")
	if !strings.Contains(strings.Join(r.calls, "\n"), "nsenter -t 1234 -n iw phy#7 set name pps4e0d5145c9") {
		t.Fatalf("关机前要按 wiphy 编号改回原名：%v", r.calls)
	}
	r = &phyRun{}
	RestorePhyName(context.Background(), r, "0", "pps1")
	if len(r.calls) != 0 {
		t.Fatalf("没有进程号就什么都不做：%v", r.calls)
	}
}
