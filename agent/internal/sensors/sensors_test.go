package sensors

import (
	"bytes"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFrames(t *testing.T) {
	var got []string
	in := []byte("pipe:qemud:sensors\x00" + "000btime:123456" + "000clist-sensors" + "0012set:acceleration:1" + "000bset-d")
	rest := Frames(in, func(s string) { got = append(got, s) })
	want := []string{"pipe:qemud:sensors", "time:123456", "list-sensors", "set:acceleration:1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
	if string(rest) != "000bset-d" {
		t.Fatalf("剩下 %q", rest)
	}
}

func parse(t *testing.T, b *bytes.Buffer) []string {
	var out []string
	rest := Frames(b.Bytes(), func(s string) { out = append(out, s) })
	if len(rest) != 0 {
		t.Fatalf("没拆完：%q", rest)
	}
	b.Reset()
	return out
}

func TestFeederProtocol(t *testing.T) {
	var b bytes.Buffer
	f := NewFeeder(Spec{Mask: DefaultMask, Seed: 7, MagWorld: MagForRegion("HK")}, &b)
	f.Handle("list-sensors")
	if got := parse(t, &b); len(got) != 1 || got[0] != strconv.Itoa(DefaultMask) {
		t.Fatalf("掩码：%v", got)
	}
	for _, n := range []string{"acceleration", "gyroscope", "magnetic-field", "proximity", "light"} {
		f.Handle("set:" + n + ":1")
	}
	f.Handle("set-delay:20")
	if f.Delay() != 20*time.Millisecond {
		t.Fatal(f.Delay())
	}
	got := parse(t, &b)
	if len(got) != 2 || got[0] != "proximity:5.0" || !strings.HasPrefix(got[1], "light:") {
		t.Fatalf("开距离 / 光线时要先报一次：%v", got)
	}
	if err := f.Tick(time.Now(), 0.02); err != nil {
		t.Fatal(err)
	}
	got = parse(t, &b)
	byName := map[string][]float64{}
	for _, m := range got {
		p := strings.Split(m, ":")
		var v []float64
		for _, x := range p[1:] {
			n, _ := strconv.ParseFloat(x, 64)
			v = append(v, n)
		}
		byName[p[0]] = v
	}
	a := byName["acceleration"]
	if len(a) != 3 || math.Abs(math.Sqrt(a[0]*a[0]+a[1]*a[1]+a[2]*a[2])-g) > 0.1 || a[2] < 9.5 {
		t.Fatalf("平放时加速度应约等于 g 且主要在 z 轴：%v", a)
	}
	if w := byName["gyroscope"]; len(w) != 3 || math.Abs(w[0]) > 0.02 {
		t.Fatalf("静止时角速度应接近 0：%v", w)
	}
	m := byName["magnetic"]
	if mag := math.Sqrt(m[0]*m[0] + m[1]*m[1] + m[2]*m[2]); mag < 40 || mag > 52 {
		t.Fatalf("地磁强度不对：%v（%.1f）", m, mag)
	}
	f.Reset()
	_ = f.Tick(time.Now(), 0.02)
	if b.Len() != 0 {
		t.Fatal("HAL 重连后，没 set 之前不该发数据")
	}
}

func TestSeedsDiffer(t *testing.T) {
	a, b := newModel(Spec{Seed: 1}), newModel(Spec{Seed: 2})
	if a.heading == b.heading || a.pitch == b.pitch {
		t.Fatal("不同手机的姿态应该不同")
	}
	if c := newModel(Spec{Seed: 1}); c.heading != a.heading {
		t.Fatal("同一台手机每次开机姿态应一样")
	}
}

func TestLuxDayNight(t *testing.T) {
	m := newModel(Spec{Seed: 1, TZOffset: 8 * 3600})
	noon := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)   // 当地 12 点
	night := time.Date(2026, 11, 16, 18, 0, 0, 0, time.UTC) // 当地 2 点
	if d, n := m.luxAt(noon), m.luxAt(night); d < 150 || n > 10 {
		t.Fatalf("白天 %.0f 夜里 %.0f", d, n)
	}
}
