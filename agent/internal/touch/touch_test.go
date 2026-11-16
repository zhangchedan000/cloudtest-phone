package touch

import (
	"encoding/binary"
	"io"
	"math/rand"
	"os"
	"testing"
)

type ev struct {
	typ, code uint16
	val       int32
}

func fakeDevice(t *testing.T) (*Device, func() []ev) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	d := &Device{f: w, cfg: Config{W: 1080, H: 2400}, rnd: rand.New(rand.NewSource(1)), next: 1}
	return d, func() []ev {
		w.Close()
		raw, _ := io.ReadAll(r)
		var out []ev
		for i := 0; i+24 <= len(raw); i += 24 {
			out = append(out, ev{binary.LittleEndian.Uint16(raw[i+16:]), binary.LittleEndian.Uint16(raw[i+18:]), int32(binary.LittleEndian.Uint32(raw[i+20:]))})
		}
		return out
	}
}

func TestTapLooksLikeAFinger(t *testing.T) {
	d, read := fakeDevice(t)
	if err := d.Do("tap 540 1200"); err != nil {
		t.Fatal(err)
	}
	evs := read()
	var downs, ups, syncs int
	var xs []int32
	var pressures = map[int32]bool{}
	for _, e := range evs {
		switch {
		case e.typ == evKey && e.code == btnTouch && e.val == 1:
			downs++
		case e.typ == evKey && e.code == btnTouch && e.val == 0:
			ups++
		case e.typ == evSyn:
			syncs++
		case e.typ == evAbs && e.code == absMTPositionX:
			xs = append(xs, e.val)
		case e.typ == evAbs && e.code == absMTPressure:
			pressures[e.val] = true
		}
	}
	if downs != 1 || ups != 1 || syncs < 3 {
		t.Fatalf("按下 %d 抬起 %d 帧 %d", downs, ups, syncs)
	}
	for _, x := range xs {
		if x < 530 || x > 550 {
			t.Fatalf("落点偏太多：%v", xs)
		}
	}
	if len(pressures) < 2 {
		t.Fatalf("按住时力度应有变化：%v", pressures)
	}
}

func TestSwipeMovesAlongPath(t *testing.T) {
	d, read := fakeDevice(t)
	if err := d.Do("swipe 540 2000 540 600 200"); err != nil {
		t.Fatal(err)
	}
	var ys []int32
	for _, e := range read() {
		if e.typ == evAbs && e.code == absMTPositionY {
			ys = append(ys, e.val)
		}
	}
	if len(ys) < 20 {
		t.Fatalf("200ms 按 120Hz 应有 20 多个点，只有 %d", len(ys))
	}
	if ys[0] < 1990 || ys[len(ys)-1] > 610 {
		t.Fatalf("起点 %d 终点 %d", ys[0], ys[len(ys)-1])
	}
	// 先快后慢：前一半走的距离明显多于后一半
	mid := ys[len(ys)/2]
	if (ys[0] - mid) < 2*(mid-ys[len(ys)-1]) {
		t.Fatalf("应该先快后慢：%d → %d → %d", ys[0], mid, ys[len(ys)-1])
	}
}

func TestBadCommands(t *testing.T) {
	d, _ := fakeDevice(t)
	for _, c := range []string{"", "tap 1", "tap a b", "rm -rf /", "swipe 1 2 3 4"} {
		if err := d.Do(c); err == nil {
			t.Fatalf("%q 应该报错", c)
		}
	}
}
