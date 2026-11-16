package phones

import "testing"

func TestParseScreen(t *testing.T) {
	out := "Physical size: 1220x2712\n      Viewport INTERNAL: displayId=0, uniqueId=local:0, port=0, orientation=1, logicalFrame=[0, 0, 2712, 1220], physicalFrame=[0, 0, 2712, 1220], deviceSize=[2712, 1220], isActive=[1]\n"
	st, err := parseScreen(out)
	if err != nil || st.w != 1220 || st.h != 2712 || st.rotation != 1 || !st.landscape() {
		t.Fatalf("%+v %v", st, err)
	}
	st, err = parseScreen("Physical size: 1080x2400\nOverride size: 720x1600\n")
	if err != nil || st.w != 720 || st.h != 1600 || st.rotation != 0 {
		t.Fatalf("改过分辨率要用改过的：%+v %v", st, err)
	}
	if _, err := parseScreen("garbage"); err == nil {
		t.Fatal("读不到尺寸应该报错")
	}
}

// 和安卓 TouchInputMapper 的换算（物理 → 画面）互逆：画面四个角对应物理的哪个角。
func TestToPhysical(t *testing.T) {
	const w, h = 1220, 2712
	fwd := func(rot, px, py int) (int, int) { // 安卓的换算：物理 → 画面
		switch rot {
		case 1:
			return py, w - px
		case 2:
			return w - px, h - py
		case 3:
			return h - py, px
		}
		return px, py
	}
	for rot := 0; rot < 4; rot++ {
		st := screenState{rotation: rot, w: w, h: h}
		for _, p := range [][2]int{{0, 0}, {100, 2000}, {1219, 5}, {610, 1356}} {
			lx, ly := fwd(rot, p[0], p[1])
			if x, y := st.toPhysical(lx, ly); x != p[0] || y != p[1] {
				t.Fatalf("方向 %d：物理 %v → 画面 (%d,%d) → 物理 (%d,%d)", rot, p, lx, ly, x, y)
			}
		}
	}
	// 横屏（向左转）：画面左上角是手机竖着时的右上角
	if x, y := (screenState{rotation: 1, w: w, h: h}).toPhysical(0, 0); x != w || y != 0 {
		t.Fatalf("横屏左上角 → (%d,%d)", x, y)
	}
}
