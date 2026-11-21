package phones

import (
	"context"
	"fmt"
	"github.com/zhangchedan000/prism-phone/agent/internal/hostguard"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 开机后设置：时区、语言。
// 实测（踩坑 #6）：persist.* 属性用 Redroid 启动参数设不进去，只能等安卓开机完成后在容器里 setprop。
// 语言要重启安卓框架（zygote）才生效，所以只在和当前值不同时设置，避免每次开机都多重启一次。

// SettingsStatus 一台设备开机后设置的结果（给管理页看）。
type SettingsStatus struct {
	State   string    `json:"state"` // pending / applied / failed
	Applied []string  `json:"applied,omitempty"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
}

// liveProps 开机后才能设置的字段 → 属性名。值必须匹配对应格式（也防止把奇怪的字符传进容器）。
var liveProps = map[string]struct {
	prop    string
	pattern *regexp.Regexp
	restart bool
}{
	"timezone": {"persist.sys.timezone", regexp.MustCompile(`^[A-Za-z]+(?:/[A-Za-z0-9_+-]+){0,2}$`), false},
	"locale":   {"persist.sys.locale", regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Za-z0-9]{2,8}){0,2}$`), true},
}

func isLiveProp(key string) bool {
	_, ok := liveProps[key]
	return ok
}

// batteryHealthCode 机型档案里的健康度 → Android BatteryManager 的 health 数值。

// applyBattery 开机后用 dumpsys battery 覆盖电池状态。dumpsys battery set 会让电池进入
// 「人工覆盖」模式，值固定到下次重启或 reset；applySettings 每次开机都跑，所以每次开机重新覆盖。
// 返回非空表示有失败项。
func (s *Service) applyBattery(ctx context.Context, id, name string, v map[string]any) []string {
	var probs []string
	run := func(args ...string) {
		full := append([]string{"exec", name, "dumpsys", "battery"}, args...)
		if out, err := s.run.Run(ctx, "docker", full...); err != nil {
			probs = append(probs, fmt.Sprintf("dumpsys battery %s 失败：%v %s", strings.Join(args, " "), err, out))
		}
	}
	// 记下起始状态，后台漂移循环（battery.go）从这里接着慢慢变。
	seedLevel, seedTemp := batterySeed(v)
	s.seedBattery(id, seedLevel, seedTemp, fmt.Sprint(v["batteryCharging"]))
	// 电量一定要先设（档案没填就用起始值 50）：Redroid 刚开机电量是 0，后面设 present=1 时
	// 如果还是 0 且没插电，安卓会当成没电马上关机（实测 3 秒内容器退出，踩坑 #47）。
	run("set", "level", strconv.Itoa(seedLevel))
	// 充电状态：设插口的同时把 status 设一致（真机充电=2 充电中，拔了=3 放电中），
	// 否则会出现「USB 供电 true 但 status 未知」这种自相矛盾、容易被看出来。
	switch fmt.Sprint(v["batteryCharging"]) {
	case "ac":
		run("set", "ac", "1")
		run("set", "usb", "0")
		run("set", "status", "2")
	case "usb":
		run("set", "usb", "1")
		run("set", "ac", "0")
		run("set", "status", "2")
	case "none":
		run("unplug")
		run("set", "status", "3")
	}
	if t, ok := toFloat(v["batteryTemp"]); ok && t > -50 && t < 150 {
		// dumpsys battery 的温度单位是 0.1℃。
		run("set", "temp", strconv.Itoa(int(t*10)))
	}
	// 电池「在位」：Redroid 没有电池 HAL，默认 present=false（真机永远是 true）。
	// 必须放在设电量之后（见上）。
	run("set", "present", "1")
	// 健康度 / 电压 / 电池类型（technology）：安卓 14 的 dumpsys battery 不支持设置（会提示 Unknown set option
	// 但仍返回成功），实测设不上，所以不发命令、不假装设过。要等镜像阶段补一个假的电池 HAL（踩坑 #47）。
	return probs
}

// toInt / toFloat：JSON 数字是 float64，统一转一下。
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

// safeSettingValue 允许写进 settings 的值：可见 ASCII + 常见符号，不含控制字符，长度有限。
// settings put 的参数不过 shell（docker exec 直接带参），这里主要挡控制字符和超长值。
func safeSettingValue(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// waitBooted 等安卓开机完成；超时或设备停了返回 false。
func (s *Service) waitBooted(ctx context.Context, id string) bool {
	deadline := time.Now().Add(s.bootTimeout())
	for {
		switch s.androidState(ctx, id) {
		case "running":
			return true
		case "stopped":
			return false
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(s.pollEvery())
	}
}

// applySettings 开机完成后把时区、语言设进去。在后台跑，结果记在 settings 里。
func (s *Service) applySettings(ctx context.Context, id string) {
	s.settings.set(id, SettingsStatus{State: "pending"})
	if !s.waitBooted(ctx, id) {
		s.settings.set(id, SettingsStatus{State: "failed", Error: "等开机超时或设备已关机"})
		return
	}
	if s.cfg.HostGuard {
		hostguard.Fix() // 安卓开机刚改过宿主机的 kernel.* / vm.*（踩坑 #58），马上改回
	}
	if s.cfg.Sensors {
		s.hideBootProps(ctx, id)
	}
	// 有模拟的 SIM 卡时移动数据关着（手机走 WiFi；reference-ril 拨数据连接会去配不存在的网卡）
	if cur, _ := s.run.Run(ctx, "docker", "exec", containerName(id), "settings", "get", "global", "mobile_data"); strings.TrimSpace(cur) != "0" {
		_, _ = s.run.Run(ctx, "docker", "exec", containerName(id), "settings", "put", "global", "mobile_data", "0")
	}
	st := s.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil {
		s.settings.set(id, SettingsStatus{State: "failed", Error: err.Error()})
		return
	}
	name := containerName(id)
	var applied, problems []string
	restart := false
	// 内存信息按容器显示（lxcfs，memory.go）
	if err := s.applyLxcfs(ctx, name); err != nil {
		problems = append(problems, err.Error())
	}
	// AOSP 自带、真机没有的测试 App：对当前用户卸掉（系统分区里的不动，恢复用 pm install-existing）。
	for _, pkg := range aospOnlyPackages {
		_, _ = s.run.Run(ctx, "docker", "exec", name, "pm", "uninstall", "--user", "0", pkg)
	}
	// 虚拟 WiFi：打开并连上这台手机自己的路由器（wifi.go）
	if err := s.connectWifi(ctx, id, s.resolvedValues(*p, st)); err != nil {
		problems = append(problems, err.Error())
	}
	// USB 调试按手机的开关（默认关，adbtoggle.go）
	s.applyAdb(ctx, id)
	// /dev 里服务器的硬件节点删掉（devclean.go，踩坑 #74）
	s.cleanDev(ctx, id)
	// 蓝牙：这台有模拟的蓝牙芯片（bluetooth.go）就不管，开关随用户；没有就关掉——安卓默认 bluetooth_on=1，
	// 开机自动开蓝牙，没芯片时蓝牙 HAL 和 com.android.bluetooth 会连崩几次才放弃（踩坑 #48）。
	if cur, _ := s.run.Run(ctx, "docker", "exec", name, "settings", "get", "global", "bluetooth_on"); strings.TrimSpace(cur) != "0" && !s.btRunning(ctx, id) {
		_, _ = s.run.Run(ctx, "docker", "exec", name, "settings", "put", "global", "bluetooth_on", "0")
	}
	// 剪贴板访问提示关掉（踩坑 #62）：管理页的「剪贴板自动互通」以 shell 身份读手机剪贴板，
	// 安卓 13 起别的 App 复制的内容被 shell 读一次，屏幕上就弹「Shell 已从剪贴板粘贴」；shell 又拿不到免提示权限。
	// 这是系统设置里用户自己能关的隐私开关（设置 → 隐私 → 显示剪贴板访问通知），顺带中文打字粘贴也不弹提示了。
	if cur, _ := s.run.Run(ctx, "docker", "exec", name, "settings", "get", "secure", "clipboard_show_access_notifications"); strings.TrimSpace(cur) != "0" {
		_, _ = s.run.Run(ctx, "docker", "exec", name, "settings", "put", "secure", "clipboard_show_access_notifications", "0")
	}
	for _, prop := range cpuVariantProps {
		if cur, _ := s.run.Run(ctx, "docker", "exec", name, "getprop", prop); cur != "" && cur != cpuVariant {
			_, _ = s.run.Run(ctx, "docker", "exec", name, "setprop", prop, cpuVariant)
		}
	}
	// 关着谷歌服务、数据里却留着 GMS（以前开过谷歌服务，或手动装过）：镜像里没有 GSF，GMS 会反复崩溃（踩坑 #46）。
	// 不自动删（可能有账号数据），提示用户。
	if !p.Gapps {
		if out, _ := s.run.Run(ctx, "docker", "exec", name, "pm", "path", "com.google.android.gms"); strings.Contains(out, "/data/app/") {
			problems = append(problems, "没开谷歌服务，但数据里还留着谷歌服务（GMS），会反复崩溃：在「信息」里打开谷歌服务，或者重置这台手机")
		}
	}
	for _, r := range s.resolve(*p, st) {
		def, ok := liveProps[r.Key]
		if !ok || r.Value == nil {
			continue
		}
		want := fmt.Sprint(r.Value)
		if !def.pattern.MatchString(want) {
			problems = append(problems, fmt.Sprintf("%s 的值 %q 格式不对，没设置", r.Key, want))
			continue
		}
		current, _ := s.run.Run(ctx, "docker", "exec", name, "getprop", def.prop)
		if r.Key == "locale" && strings.TrimSpace(current) == "" {
			// 没人设过 persist.sys.locale 时系统用 ro.product.locale：开机参数里已经按代理国家给了（bootProps），
			// 新手机第一次开机就是对的语言，不用再设、也不用重启框架（踩坑 #118）。
			current, _ = s.run.Run(ctx, "docker", "exec", name, "getprop", "ro.product.locale")
		}
		if strings.TrimSpace(current) == want {
			applied = append(applied, r.Key)
			continue
		}
		if out, err := s.run.Run(ctx, "docker", "exec", name, "setprop", def.prop, want); err != nil {
			problems = append(problems, fmt.Sprintf("%s 设置失败：%v %s", r.Key, err, out))
			continue
		}
		if r.Key == "timezone" {
			// 让正在运行的系统马上换时区（不支持这个命令的版本忽略，属性已经设好，重启后也会生效）。
			_, _ = s.run.Run(ctx, "docker", "exec", name, "cmd", "time_zone_detector", "suggest_manual_time_zone", "--zone_id", want)
		}
		if def.restart && p.AndroidVersion >= 15 {
			// 安卓 15 起不能只重启框架（ctl.restart zygote）：PMS 软重启时把 apex 里的 PermissionController 等包
			// 判成「no longer exists」擦掉，之后 system_server 每次起都崩「exactly one permissions manager; found []」
			// 死循环（踩坑 #118）。语言已经写进 persist.sys.locale，下次开机（每次开机都重建容器）生效。
			applied = append(applied, r.Key+"（下次开机生效）")
			continue
		}
		applied = append(applied, r.Key)
		restart = restart || def.restart
	}
	// setting 类字段：开机后用安卓 settings put 写（android_id、设备名、蓝牙 MAC 等）。
	for _, r := range s.resolve(*p, st) {
		f, ok := s.cat.Field(r.Key)
		if !ok || f.Sink.Kind != "setting" || r.Value == nil {
			continue
		}
		val := fmt.Sprint(r.Value)
		if !safeSettingValue(val) {
			problems = append(problems, fmt.Sprintf("%s 的值 %q 含非法字符，没设置", r.Key, val))
			continue
		}
		wrote := false
		for _, sink := range f.Sink.Names {
			ns, key, found := strings.Cut(sink, ":") // 形如 "secure:android_id"
			if !found {
				continue
			}
			// 已经是目标值就跳过（避免每次开机都写一遍）。
			if cur, _ := s.run.Run(ctx, "docker", "exec", name, "settings", "get", ns, key); strings.TrimSpace(cur) == val {
				wrote = true
				continue
			}
			if out, err := s.run.Run(ctx, "docker", "exec", name, "settings", "put", ns, key, val); err != nil {
				problems = append(problems, fmt.Sprintf("%s（%s）设置失败：%v %s", r.Key, sink, err, out))
				continue
			}
			wrote = true
		}
		if wrote {
			applied = append(applied, r.Key)
		}
	}

	// battery 类字段：开机后用 dumpsys battery 覆盖（电量、充电、温度、健康）。
	batteryVals := map[string]any{}
	for _, r := range s.resolve(*p, st) {
		if f, ok := s.cat.Field(r.Key); ok && f.Sink.Kind == "battery" && r.Value != nil {
			batteryVals[r.Key] = r.Value
		}
	}
	if len(batteryVals) > 0 {
		if probs := s.applyBattery(ctx, id, name, batteryVals); len(probs) > 0 {
			problems = append(problems, probs...)
		} else {
			applied = append(applied, "电池")
		}
	}

	if restart {
		// 语言变了：重启安卓框架（不是重启容器，几十秒）。
		if out, err := s.run.Run(ctx, "docker", "exec", name, "setprop", "ctl.restart", "zygote"); err != nil {
			problems = append(problems, fmt.Sprintf("重启安卓框架失败：%v %s", err, out))
		} else {
			// 框架重启期间设备算「开机中」，等它重新就绪再报完成（踩坑 #15）。
			s.boots.forget(id)
			time.Sleep(3 * s.pollEvery())
			if !s.waitBooted(ctx, id) {
				problems = append(problems, "安卓框架重启后没有恢复")
			}
		}
	}
	// 预装应用（Chrome 等）：放在语言重启之后，免得装到一半框架被重启。
	inst, probs := s.preinstall(ctx, id)
	for _, f := range inst {
		applied = append(applied, "预装 "+f)
	}
	problems = append(problems, probs...)
	status := SettingsStatus{State: "applied", Applied: applied}
	if len(problems) > 0 {
		status.State, status.Error = "failed", strings.Join(problems, "；")
	}
	s.settings.set(id, status)
}

type settingsTracker struct{ tracker[SettingsStatus] }

func (t *settingsTracker) set(id string, st SettingsStatus) {
	st.At = time.Now().UTC()
	t.put(id, st)
}

// aospOnlyPackages 只有 AOSP / 模拟器才有的 App（出现在已装应用列表里就是差异项）。
var aospOnlyPackages = []string{"org.chromium.webview_shell"}
