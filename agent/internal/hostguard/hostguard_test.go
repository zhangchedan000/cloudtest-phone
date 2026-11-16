package hostguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFix(t *testing.T) {
	Root = t.TempDir()
	set := func(k, v string) {
		p := path(k)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(v), 0o644)
	}
	get := func(k string) string { b, _ := os.ReadFile(path(k)); return strings.TrimSpace(string(b)) }
	set("kernel.modprobe", "\n")
	set("kernel.panic_on_oops", "1\n")
	set("vm.watermark_scale_factor", "1795\n")
	set("vm.overcommit_memory", "0\n")
	fixed := Fix()
	if len(fixed) != 3 {
		t.Fatalf("应改回 3 项：%v", fixed)
	}
	if get("kernel.modprobe") != "/sbin/modprobe" || get("kernel.panic_on_oops") != "0" || get("vm.watermark_scale_factor") != "10" {
		t.Fatal("没改回")
	}
	if len(Fix()) != 0 {
		t.Fatal("第二次不该再改")
	}
	ConfFile = filepath.Join(t.TempDir(), "x.conf")
	if err := WriteConf(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(ConfFile)
	if !strings.Contains(string(b), "kernel.modprobe = /sbin/modprobe") {
		t.Fatal(string(b))
	}
}
