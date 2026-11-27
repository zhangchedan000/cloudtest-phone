package phones

import (
	"context"
	"strings"
)

// 导航栏默认手势（2026-11-27 用户要求）：安卓默认的三键导航栏（返回 / 主页 / 多任务）会占一条，
// 还会挡住一些没适配的 App。真机现在也大多默认手势导航。所以开机后统一切成手势导航
// （navigation_mode 0→2），底部只剩一条细横条、不挡 App。这是系统自带的 secure 设置、真机都有，不影响指纹。
//
// 注：手势导航底部那条细横条（gesture pill）在原生安卓里没有命令能单独藏掉（只有 threebutton/transparent/
// gestural 三个 overlay，没有「隐藏小横条」开关）；它很细、不挡 App，就留着。

// applyNavGesture 开机后把导航切成手势（已经是手势就不动）。
func (s *Service) applyNavGesture(ctx context.Context, id string) {
	name := containerName(id)
	if cur, _ := s.run.Run(ctx, "docker", "exec", name, "settings", "get", "secure", "navigation_mode"); strings.TrimSpace(cur) == "2" {
		return
	}
	// enable-exclusive：开手势 overlay 的同时把同类的三键 / 两键 overlay 关掉。
	_, _ = s.run.Run(ctx, "docker", "exec", name, "cmd", "overlay", "enable-exclusive",
		"--category", "com.android.internal.systemui.navbar.gestural")
}
