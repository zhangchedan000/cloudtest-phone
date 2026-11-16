package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCgroupUsage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "system.slice", "docker-abc.scope")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	w := func(n, v string) { _ = os.WriteFile(filepath.Join(dir, n), []byte(v), 0o644) }
	w("memory.current", "3221225472\n")
	w("memory.stat", "anon 100\ninactive_file 1073741824\nactive_file 5\n")
	w("memory.max", "8589934592\n")
	w("cpu.stat", "usage_usec 1000\nuser_usec 800\n")
	u, err := CgroupUsage(context.Background(), root, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if u.MemMB != 2048 || u.MemLimitMB != 8192 || u.CPUPercent != 0 {
		t.Fatalf("%+v", u)
	}
	w("memory.max", "max\n")
	if u, _ = CgroupUsage(context.Background(), root, "abc"); u.MemLimitMB <= 0 {
		t.Fatalf("不限内存时应报整机内存：%+v", u)
	}
	if _, err := CgroupUsage(context.Background(), root, "nope"); err == nil {
		t.Fatal("没有的容器应报错")
	}
}
