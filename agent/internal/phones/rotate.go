package phones

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// 横竖屏（投屏窗口的「旋转」按钮）。
//
// 安卓的显示方向：0 竖屏、1 向左转 90°（横屏）、2 倒过来、3 向右转 90°。转屏用 settings 的 user_rotation
// （先关自动旋转）。转了以后：
//   - 画面（screencap / screenrecord）是转过以后的尺寸（2712x1220）；
//   - 但虚拟触摸屏（internal/touch）的坐标永远是手机竖着拿时的「物理坐标」，安卓自己按方向换算。
//     所以管理页按画面发来的坐标要先换回物理坐标再交给触摸屏（input 命令不用换，它本来就按画面坐标）。
//
// 桌面（Launcher）是锁竖屏的（真手机也一样），在桌面上转不过去，打开支持横屏的 App 才会横过来；
// App 自己要横屏（游戏、视频）时方向也会变，所以方向不能只记「按过旋转」，要问安卓当前实际的方向。

// screenState 手机当前的显示方向和竖着时的分辨率。
type screenState struct {
	rotation int // 0~3
	w, h     int // 竖着拿时的宽高（物理）
	at       time.Time
}

// screenCache 每台手机的方向缓存（点一下就问一次太慢；方向变化不频繁，缓存 1.5 秒）。
type screenCache struct {
	mu sync.Mutex
	m  map[string]screenState
}

const screenCacheTTL = 1500 * time.Millisecond

var (
	reViewport = regexp.MustCompile(`Viewport INTERNAL:.*?orientation=([0-3])`)
	rePhysical = regexp.MustCompile(`Physical size: (\d+)x(\d+)`)
	reOverride = regexp.MustCompile(`Override size: (\d+)x(\d+)`)
)

// parseScreen 解析 `wm size; dumpsys input | grep Viewport` 的输出。
func parseScreen(out string) (screenState, error) {
	var st screenState
	m := reOverride.FindStringSubmatch(out)
	if m == nil {
		m = rePhysical.FindStringSubmatch(out)
	}
	if m == nil {
		return st, fmt.Errorf("读不到屏幕尺寸：%q", out)
	}
	st.w, _ = strconv.Atoi(m[1])
	st.h, _ = strconv.Atoi(m[2])
	if v := reViewport.FindStringSubmatch(out); v != nil {
		st.rotation, _ = strconv.Atoi(v[1])
	}
	return st, nil
}

// screen 手机当前的方向（fresh=true 不用缓存）。
func (s *Service) screen(ctx context.Context, id string, fresh bool) (screenState, error) {
	s.screens.mu.Lock()
	st, ok := s.screens.m[id]
	s.screens.mu.Unlock()
	if ok && !fresh && time.Since(st.at) < screenCacheTTL {
		return st, nil
	}
	out, err := s.run.Run(ctx, "docker", "exec", containerName(id), "sh", "-c", "wm size; dumpsys input | grep -m1 'Viewport INTERNAL'")
	if err != nil {
		return st, fmt.Errorf("读屏幕方向失败：%v %s", err, out)
	}
	st, err = parseScreen(out)
	if err != nil {
		return st, err
	}
	st.at = time.Now()
	s.screens.mu.Lock()
	if s.screens.m == nil {
		s.screens.m = map[string]screenState{}
	}
	s.screens.m[id] = st
	s.screens.mu.Unlock()
	return st, nil
}

// landscape 当前是不是横着的。
func (st screenState) landscape() bool { return st.rotation%2 == 1 }

// toPhysical 画面坐标（转过以后看到的）→ 触摸屏的物理坐标（和安卓 TouchInputMapper 的换算反过来）。
func (st screenState) toPhysical(x, y int) (int, int) {
	switch st.rotation {
	case 1: // 画面 x = 物理 y，画面 y = 宽 - 物理 x
		return st.w - y, x
	case 2:
		return st.w - x, st.h - y
	case 3: // 画面 x = 高 - 物理 y，画面 y = 物理 x
		return y, st.h - x
	}
	return x, y
}

// Rotate 横竖屏切换：竖着就转成横屏（user_rotation=1），横着就转回竖屏。
// 返回想转成横屏没有、转完以后实际是不是横的（桌面等锁竖屏的界面转不过去）。
func (s *Service) Rotate(ctx context.Context, id string) (wantLandscape, landscape bool, err error) {
	if err := s.requireRunning(ctx, id, true); err != nil {
		return false, false, err
	}
	st, err := s.screen(ctx, id, true)
	if err != nil {
		return false, false, err
	}
	want := 1
	if st.landscape() {
		want = 0
	}
	cmd := fmt.Sprintf("settings put system accelerometer_rotation 0; settings put system user_rotation %d", want)
	if out, err := s.run.Run(ctx, "docker", "exec", containerName(id), "sh", "-c", cmd); err != nil {
		return false, false, fmt.Errorf("旋转失败：%v %s", err, out)
	}
	// 安卓转屏有动画，等它转完再看结果
	for i := 0; i < 8; i++ {
		time.Sleep(250 * time.Millisecond)
		if st, err = s.screen(ctx, id, true); err == nil && st.landscape() == (want == 1) {
			break
		}
	}
	return want == 1, st.landscape(), nil
}
