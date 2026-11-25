package fakefs

import (
	"strings"
	"testing"
)

const hostCPU = `processor	: 0
BogoMIPS	: 50.00
Features	: fp asimd evtstrm aes pmull sha1 sha2 crc32 atomics fphp asimdhp cpuid asimdrdm lrcpc dcpop asimddp ssbs
CPU implementer	: 0x41
CPU architecture: 8
CPU variant	: 0x3
CPU part	: 0xd0c
CPU revision	: 1
`

func TestCPUInfoKeepsHostFeaturesChangesIdentity(t *testing.T) {
	files, err := Build(Spec{
		CPUParts:    []CPUPart{{Part: "Cortex-A55", MIDR: "0x412fd050", Count: 2}, {Part: "Cortex-A78", MIDR: "0x411fd410", Count: 2}},
		HostCPUInfo: hostCPU,
	})
	if err != nil {
		t.Fatal(err)
	}
	var cpuinfo string
	var midrs []string
	for _, f := range files {
		if f.Target == "/proc/cpuinfo" {
			cpuinfo = f.Content
		}
		if strings.HasSuffix(f.Target, "midr_el1") {
			midrs = append(midrs, f.Content)
		}
	}
	if strings.Count(cpuinfo, "processor\t:") != 4 {
		t.Fatalf("应该 4 个核：\n%s", cpuinfo)
	}
	for _, want := range []string{"CPU part\t: 0xd05", "CPU part\t: 0xd41", "CPU variant\t: 0x2", "CPU variant\t: 0x1", "Features\t: fp asimd evtstrm", "BogoMIPS\t: 50.00"} {
		if !strings.Contains(cpuinfo, want) {
			t.Errorf("cpuinfo 缺少 %q", want)
		}
	}
	if strings.Contains(cpuinfo, "0xd0c") {
		t.Error("不能留下宿主机真实 CPU 型号（Neoverse-N1 0xd0c）")
	}
	if len(midrs) != 4 || midrs[0] != "0x00000000412fd050\n" || midrs[3] != "0x00000000411fd410\n" {
		t.Fatalf("midr_el1 不对：%q", midrs)
	}
}

func TestProcVersionMatchesKernelSeries(t *testing.T) {
	v := ProcVersion("5.10.198-android12-9-gc2f7e1a40b6d", "#1 SMP PREEMPT Mon Jun 3 08:12:45 UTC 2025")
	if !strings.HasPrefix(v, "Linux version 5.10.198-android12-9-gc2f7e1a40b6d (build-user@build-host)") || !strings.Contains(v, "clang version 12") {
		t.Fatalf("5.10 内核格式不对：%s", v)
	}
	if v := ProcVersion("6.1.75-android14-11-gabc", "#1"); !strings.Contains(v, "clang version 17") {
		t.Fatalf("6.1 应该用 clang 17：%s", v)
	}
}

func TestEmptySpecOnlyMakesCmdline(t *testing.T) {
	// 空 Spec 没有机型数据，只生成通用的 /proc/cmdline（擦掉云主机内核命令行）。
	files, err := Build(Spec{})
	if err != nil || len(files) != 1 || files[0].Target != "/proc/cmdline" {
		t.Fatalf("空 Spec 应只生成 /proc/cmdline：%v %v", files, err)
	}
	if strings.Contains(files[0].Content, "oracle") || strings.Contains(files[0].Content, "cloudimg") {
		t.Fatalf("cmdline 不该含云主机痕迹：%s", files[0].Content)
	}
}

func TestBootID(t *testing.T) {
	files, err := Build(Spec{BootID: "0f1e2d3c-4b5a-4698-8776-5a4b3c2d1e0f"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Target == "/proc/sys/kernel/random/boot_id" && f.Content == "0f1e2d3c-4b5a-4698-8776-5a4b3c2d1e0f\n" {
			return
		}
	}
	t.Fatal("没有 boot_id")
}

func TestDeviceTreeMasking(t *testing.T) {
	// 盖住宿主机真板子的 /proc/device-tree model/compatible（Linux Q1 / rockchip,rk3588，踩坑 #157）。
	files, err := Build(Spec{DeviceTreeModel: "Phone (2a)", DeviceTreeCompatible: "mediatek,mt6886"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Target] = f.Content
	}
	// /proc 和 /sys 两条路径都要盖（/proc/device-tree 可能是 /sys/firmware/devicetree/base 的软链）。
	want := map[string]string{
		"/proc/device-tree/model":                  "Phone (2a)\x00",
		"/sys/firmware/devicetree/base/model":      "Phone (2a)\x00",
		"/proc/device-tree/compatible":             "mediatek,mt6886\x00",
		"/sys/firmware/devicetree/base/compatible": "mediatek,mt6886\x00",
	}
	for target, content := range want {
		if got[target] != content {
			t.Errorf("%s = %q，要 %q", target, got[target], content)
		}
	}
	for _, f := range files {
		if strings.Contains(f.Content, "rockchip") || strings.Contains(f.Content, "Q1") {
			t.Errorf("%s 还露着宿主机板子：%q", f.Target, f.Content)
		}
	}
	// 文件名要唯一（写进同一个目录，重名会互相覆盖）。
	names := map[string]bool{}
	for _, f := range files {
		if f.Name == "" {
			continue
		}
		if names[f.Name] {
			t.Errorf("文件名重复：%s", f.Name)
		}
		names[f.Name] = true
	}
	// 留空就不生成（别造半真文件）。
	files2, _ := Build(Spec{})
	for _, f := range files2 {
		if strings.Contains(f.Target, "device-tree") || strings.Contains(f.Target, "devicetree") {
			t.Errorf("空 Spec 不该生成 device-tree 文件：%s", f.Target)
		}
	}
}
