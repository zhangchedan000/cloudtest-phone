package fakefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testTree() *CPUTree {
	t := &CPUTree{Name: "test", Files: map[string]string{}, Links: map[string]string{}}
	for i := 0; i < 8; i++ {
		midr := "0x00000000412fd050\n" // A55
		pol := "policy0"
		if i >= 4 {
			midr, pol = "0x00000000411fd410\n", "policy4" // A78
		}
		p := "cpu" + string(rune('0'+i))
		t.Files[p+"/regs/identification/midr_el1"] = midr
		t.Files[p+"/online"] = "1\n"
		t.Links[p+"/cpufreq"] = "../cpufreq/" + pol
	}
	t.Files["online"] = "0-7\n"
	t.Files["cpufreq/policy0/scaling_driver"] = "mtk-cpufreq-hw\n"
	t.Files["cpufreq/policy4/cpuinfo_max_freq"] = "2850000\n"
	t.Dirs = []string{"cpufreq/policy0/stats"}
	return t
}

// 有 CPU 模板：cpuinfo 的核心型号按模板 midr 出（和整个目录一致），不再单独生成 midr / 频率文件。
func TestBuildWithCPUTree(t *testing.T) {
	host := "processor\t: 0\nBogoMIPS\t: 48.00\nFeatures\t: fp asimd aes\n"
	files, err := Build(Spec{CPUTree: testTree(), HostCPUInfo: host, CPUMaxFreqKHz: []int{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	var cpuinfo string
	var tree bool
	for _, f := range files {
		switch {
		case f.Target == "/proc/cpuinfo":
			cpuinfo = f.Content
		case f.Target == "/sys/devices/system/cpu" && f.Tree != nil:
			tree = true
		case strings.Contains(f.Target, "midr_el1") || strings.Contains(f.Target, "max_freq"):
			t.Fatalf("有模板时不该再单独生成 %s", f.Target)
		}
	}
	if !tree {
		t.Fatal("没有整个 CPU 目录")
	}
	if strings.Count(cpuinfo, "CPU part\t: 0xd05") != 4 || strings.Count(cpuinfo, "CPU part\t: 0xd41") != 4 || !strings.Contains(cpuinfo, "Features\t: fp asimd aes") {
		t.Fatalf("cpuinfo 不对：\n%s", cpuinfo)
	}
}

// 写出来是真目录：文件内容、权限、链接都照模板；坏路径拒绝。
func TestCPUTreeWrite(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, []File{{Name: "cpu", Target: "/sys/devices/system/cpu", Tree: testTree()}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "cpu/cpufreq/policy0/scaling_driver"))
	if string(b) != "mtk-cpufreq-hw\n" {
		t.Fatalf("内容 %q", b)
	}
	if l, err := os.Readlink(filepath.Join(dir, "cpu/cpu5/cpufreq")); err != nil || l != "../cpufreq/policy4" {
		t.Fatalf("链接 %q %v", l, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "cpu/cpu5/cpufreq/cpuinfo_max_freq")); string(b) != "2850000\n" {
		t.Fatalf("顺着链接读 %q", b)
	}
	fi, _ := os.Stat(filepath.Join(dir, "cpu/online"))
	if fi.Mode().Perm() != 0o644 || !fi.Mode().IsRegular() {
		t.Fatalf("online 权限 %v", fi.Mode())
	}
	fi, _ = os.Stat(filepath.Join(dir, "cpu/cpu0/regs/identification/midr_el1"))
	if fi.Mode().Perm() != 0o444 {
		t.Fatalf("midr 权限 %v", fi.Mode())
	}
	if fi, err := os.Stat(filepath.Join(dir, "cpu/cpufreq/policy0/stats")); err != nil || !fi.IsDir() {
		t.Fatal("空目录没建")
	}
	bad := testTree()
	bad.Files["../../etc/passwd"] = "x"
	if err := bad.Validate(); err == nil {
		t.Fatal("../ 路径应该拒绝")
	}
}
