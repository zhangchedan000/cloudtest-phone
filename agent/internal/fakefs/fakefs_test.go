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

func TestEmptySpecMakesNothing(t *testing.T) {
	files, err := Build(Spec{})
	if err != nil || len(files) != 0 {
		t.Fatalf("没数据不该生成文件：%v %v", files, err)
	}
}
