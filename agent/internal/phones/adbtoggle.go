package phones

import (
	"context"
	"strings"
)

// USB 调试开关（踩坑 #56）：Redroid 默认 adb_enabled=1，App 读 Settings.Global.ADB_ENABLED 就知道
// 「开发者选项 → USB 调试」开着，真手机普通用户都是关的。现在每台手机一个开关，默认关；
// 关着时 adbd 不运行（实测：settings put global adb_enabled 0 → init.svc.adbd=stopped，adb connect 被拒）。
// 管家自己操作手机用 docker exec，不依赖 ADB。

// applyAdb 按手机的开关设 USB 调试（已经是目标值就不动）。开着的手机切换开关时马上调用；开机设置里也调用。
func (s *Service) applyAdb(ctx context.Context, id string) {
	st := s.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil || !s.isRunning(ctx, id) {
		return
	}
	want := "0"
	if p.Adb {
		want = "1"
	}
	name := containerName(id)
	if cur, _ := s.run.Run(ctx, "docker", "exec", name, "settings", "get", "global", "adb_enabled"); strings.TrimSpace(cur) != want {
		_, _ = s.run.Run(ctx, "docker", "exec", name, "settings", "put", "global", "adb_enabled", want)
	}
}
