package phones

import (
	"reflect"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/fakefs"
)

func TestFitCPUsKeepsBigCores(t *testing.T) {
	parts := []fakefs.CPUPart{{Part: "A55", Count: 4}, {Part: "A78", Count: 3}, {Part: "X2", Count: 1}}
	freqs := []int{1800, 1800, 1800, 1800, 2850, 2850, 2850, 3200}
	got, f := fitCPUs(parts, freqs, 4)
	counts := []int{}
	for _, p := range got {
		counts = append(counts, p.Count)
	}
	if !reflect.DeepEqual(counts, []int{2, 1, 1}) || !reflect.DeepEqual(f, []int{1800, 1800, 2850, 3200}) {
		t.Fatalf("4 核应为 2+1+1 且频率跟着走：%v %v", counts, f)
	}
	got, f = fitCPUs(parts, freqs, 2)
	if len(got) != 2 || got[0].Part != "A78" || got[1].Part != "X2" || !reflect.DeepEqual(f, []int{2850, 3200}) {
		t.Fatalf("2 核应留大核：%v %v", got, f)
	}
	got, _ = fitCPUs(parts, freqs, 8)
	if !reflect.DeepEqual(got, parts) {
		t.Fatal("核够时不该变")
	}
	two := []fakefs.CPUPart{{Part: "A55", Count: 4}, {Part: "A78", Count: 4}}
	got, _ = fitCPUs(two, nil, 4)
	if got[0].Count != 2 || got[1].Count != 2 {
		t.Fatalf("4+4 缩到 4 应为 2+2：%v", got)
	}
}
