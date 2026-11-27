package phones

import "context"

// applyLauncherHomeIcons 打开原生桌面 Launcher3 的「新装 App 自动摆到主屏幕」开关
// （SharedPreferences 的 pref_add_icon_to_home）。
//
// 2026-11-27 用户要求：机型都是海外/原生款，桌面就用原生 Launcher3（带应用抽屉）。但原生默认
// 新装的 App 只进抽屉、不上桌面，手机看着太空、不真实。打开这个开关后，装 App（Play 商店、
// 以及走安装会话的）会像 Pixel 一样：既进抽屉、也在桌面自动摆一个图标。
//
// 做法：直接改 Launcher3 的 prefs xml（在 </map> 前插一行），保持属主，然后 force-stop 桌面让它重读。
// 幂等：已经有这个键就跳过（不重复改、也不每次重启桌面）。只对用 Launcher3 的镜像有效（prefs 文件在才处理；
// lineage 的 Trebuchet 桌面没有这个文件，自动跳过）。
func (s *Service) applyLauncherHomeIcons(ctx context.Context, id string) {
	// 注意：am force-stop / cmd activity 在容器里连不上 activity 服务（docker exec 命名空间，#171 一家），
	// 所以让桌面重读设置用 kill 进程（系统会自动把桌面拉起来），不用 am。
	const script = `
P=/data/data/com.android.launcher3/shared_prefs/com.android.launcher3.prefs.xml
[ -f "$P" ] || exit 0
grep -q 'name="pref_add_icon_to_home"' "$P" && exit 0
owner=$(stat -c '%U:%G' "$P" 2>/dev/null)
sed -i 's#</map>#    <boolean name="pref_add_icon_to_home" value="true" />\n</map>#' "$P" || exit 0
[ -n "$owner" ] && chown "$owner" "$P" 2>/dev/null
pid=$(pidof com.android.launcher3); [ -n "$pid" ] && kill $pid 2>/dev/null || true
`
	_, _ = s.run.Run(ctx, "docker", "exec", containerName(id), "sh", "-c", script)
}
