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
	want, usb := "0", "mtp"
	if p.Adb {
		want, usb = "1", "mtp,adb"
	}
	name := containerName(id)
	if cur, _ := s.run.Run(ctx, "docker", "exec", name, "settings", "get", "global", "adb_enabled"); strings.TrimSpace(cur) != want {
		_, _ = s.run.Run(ctx, "docker", "exec", name, "settings", "put", "global", "adb_enabled", want)
	}
	// USB 模式属性跟着开关走：真手机关调试时是 mtp、开着是 mtp,adb；Redroid 默认一直是 adb。
	// 实测（踩坑 #56）：三个都改不影响 adbd 的启停（由 adb_enabled 决定），重启后开关照常生效。
	for _, prop := range []string{"persist.sys.usb.config", "sys.usb.config", "sys.usb.state"} {
		if cur, _ := s.run.Run(ctx, "docker", "exec", name, "getprop", prop); strings.TrimSpace(cur) != usb {
			_, _ = s.run.Run(ctx, "docker", "exec", name, "setprop", prop, usb)
		}
	}
}
