package phones

import (
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/modem"
	"github.com/zhangchedan000/prism-phone/agent/internal/sensors"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
	"github.com/zhangchedan000/prism-phone/agent/internal/touch"
)

// 传感器（踩坑 #59）：镜像带传感器 HAL（标签 prism.sensors=1）时，pp-init 在安卓开机前起一个喂数据的小程序，
// 扮演模拟器给 HAL 发数据（internal/sensors）。数据按这台手机的种子、代理出口地区（地磁）和时区（室内光线）生成。

func (s *Service) imageHasSensors(ctx context.Context, image string) bool {
	return s.imageLabel(ctx, image, "prism.sensors") == "1"
}

// imageLabel 镜像标签的值（没有返回空）。
func (s *Service) imageLabel(ctx context.Context, image, key string) string {
	out, err := s.run.Run(ctx, "docker", "image", "inspect", "-f", `{{index .Config.Labels "`+key+`"}}`, image)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// simSpec 这台手机的 SIM 卡（踩坑 #65）：运营商按 SIM 国家（跟随代理出口）选，IMEI / IMSI / ICCID 按 androidId 播种
// （一键新机换 androidId，就换卡）。
// 不能和别的手机标识重复（踩坑 #79）：IMEI 前 8 位是机型型号码，后面只有 6 位随机——同一机型开几百台就可能撞上。
// 跟同一台服务器上比它先建的手机（ID 更小）的号比，IMEI / IMSI / ICCID 任何一个撞了就换个种子再生成；规则固定，
// 每次开机算出来一样。
func (s *Service) simSpec(p store.Phone, st store.State, byKey map[string]any) *modem.Spec {
	// 按 ID 从小到大依次分配：每台都跟前面所有手机「最终用的号」比（不只是原来的号，种子一样时也不会连环撞）
	var earlier []store.Phone
	for _, o := range st.Phones {
		if o.ID < p.ID {
			earlier = append(earlier, o)
		}
	}
	sort.Slice(earlier, func(i, j int) bool { return earlier[i].ID < earlier[j].ID })
	taken := map[string]bool{}
	assign := func(ph store.Phone, keys map[string]any) modem.Spec {
		var sp modem.Spec
		for try := 0; try < 20; try++ {
			sp = simPick(ph, st, keys, try, s.modelTacs(ph.ModelID))
			if !taken[sp.IMEI] && (sp.Absent || (!taken[sp.IMSI] && !taken[sp.ICCID])) {
				break
			}
		}
		taken[sp.IMEI] = true
		if !sp.Absent {
			taken[sp.IMSI], taken[sp.ICCID] = true, true
		}
		return sp
	}
	for _, o := range earlier {
		keys := map[string]any{}
		for _, r := range s.resolve(o, st) {
			keys[r.Key] = r.Value
		}
		assign(o, keys)
	}
	spec := assign(p, byKey)
	// 基带版本照真机（检测器 #154：以前高通机型也报联发科的 MOLY…）
	if m := s.model(p.ModelID); m != nil {
		if v := strings.SplitN(m.RadioProps["gsm.version.baseband"], ",", 2)[0]; v != "" && len(v) <= 90 {
			spec.Version = v
		}
	}
	return &spec
}

// modelTacs 机型档案里的真实 TAC（没有机型 / 没填返回 nil）。
func (s *Service) modelTacs(modelID string) []string {
	if m := s.model(modelID); m != nil {
		return m.ImeiTacs
	}
	return nil
}

// simPick 第 try 次生成（try=0 就是原来的号，老手机号码不变）。tacs 是机型的真实 TAC：按 androidId 固定挑一个
// （不随 try 变：换种子避撞号时机型码不动，只换后 6 位）。
func simPick(p store.Phone, st store.State, byKey map[string]any, try int, tacs []string) modem.Spec {
	country, _ := byKey["simCountry"].(string)
	if country == "" {
		if px := st.Proxy(p.ProxyID); px != nil && px.Check != nil {
			country = px.Check.Country
		}
	}
	seedSrc := str(byKey["androidId"])
	if seedSrc == "" {
		seedSrc = p.ID
	}
	h := fnv.New64a()
	h.Write([]byte("sim:" + seedSrc))
	if try > 0 {
		fmt.Fprintf(h, "#%d", try)
	}
	tac, _ := byKey["imeiTac"].(string)
	if len(tac) != 8 && len(tacs) > 0 {
		th := fnv.New64a()
		th.Write([]byte("tac:" + seedSrc))
		tac = tacs[th.Sum64()%uint64(len(tacs))]
	}
	return modem.Pick(country, tac, int64(h.Sum64()>>1))
}

// sensorSpec 这台手机的传感器配置。
func sensorSpec(p store.Phone, st store.State, byKey map[string]any) sensors.Spec {
	h := fnv.New64a()
	h.Write([]byte(p.ID))
	spec := sensors.Spec{Mask: sensors.DefaultMask, Seed: int64(h.Sum64() >> 1)}
	country, tz := "", ""
	if px := st.Proxy(p.ProxyID); px != nil && px.Check != nil {
		country, tz = px.Check.Country, px.Check.Timezone
	}
	spec.MagWorld = sensors.MagForRegion(country)
	// 电池的 sysfs（踩坑 #78）：起始值和开机后 dumpsys battery set 的一样
	lv, temp := batterySeed(byKey)
	spec.Battery = &sensors.Battery{Level: lv, TempDeci: temp, Charging: str(byKey["batteryCharging"])}
	// 热区的 sysfs（踩坑 #86）：温度在电池温度（0.1℃ → 毫℃，×100）附近按种子生成，每台不同
	spec.Thermal = sensors.ThermalZonesFor(spec.Seed, temp*100)
	if size, ok := byKey["resolution"].(map[string]any); ok {
		w, _ := toInt(size["w"])
		h, _ := toInt(size["h"])
		spec.W, spec.H = w, h
	}
	if name, ok := byKey["touchName"].(string); ok && name != "" {
		spec.TouchName = name
	}
	if loc, err := time.LoadLocation(tz); err == nil && tz != "" {
		_, spec.TZOffset = time.Now().In(loc).Zone()
	} else {
		spec.TZOffset = 8 * 3600
	}
	return spec
}

// withSensors 把传感器参数插进 pp-init 的参数（[启动器, "pp-init", 开机时长, 安卓 init...]）。
func withSensors(entrypoint []string, spec sensors.Spec) []string {
	if len(entrypoint) < 2 || entrypoint[1] != "pp-init" {
		return entrypoint
	}
	out := append([]string{}, entrypoint[:2]...)
	out = append(out, "--sensors="+spec.Encode())
	return append(out, entrypoint[2:]...)
}

// touchInput 把点击 / 滑动发给手机里的小助手，由虚拟触摸屏像真手指一样做出来。
func (s *Service) touchInput(ctx context.Context, id string, req InputRequest) error {
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	if err != nil || info.State.Pid <= 0 {
		return fmt.Errorf("拿不到容器进程：%v", err)
	}
	ns := fmt.Sprintf("/proc/%d/ns/net", info.State.Pid)
	// 触摸屏用竖着拿时的物理坐标；横屏时管理页发来的是转过以后的画面坐标，要换回去（rotate.go）
	x, y, x2, y2 := req.X, req.Y, req.X2, req.Y2
	if st, err := s.screen(ctx, id, false); err == nil && st.rotation != 0 {
		x, y = st.toPhysical(x, y)
		x2, y2 = st.toPhysical(x2, y2)
	}
	switch req.Kind {
	case "tap":
		return touch.Send(ns, fmt.Sprintf("tap %d %d", x, y), 3*time.Second)
	case "swipe":
		d := req.Duration
		if d <= 0 {
			d = 300
		}
		return touch.Send(ns, fmt.Sprintf("swipe %d %d %d %d %d", x, y, x2, y2, d), time.Duration(d)*time.Millisecond+3*time.Second)
	}
	return fmt.Errorf("不是触摸操作")
}

// bootOnlyProps 只在开机时有用、带 Redroid 痕迹的属性：开机后藏起来（踩坑 #63）。
// Redroid 的显示 / 显卡驱动只在启动时读一次它们；SurfaceFlinger 万一重启会按默认值来（极少见）。
var bootOnlyProps = []string{
	"ro.boot.redroid_width", "ro.boot.redroid_height", "ro.boot.redroid_dpi", "ro.boot.redroid_fps",
	"ro.boot.redroid_gpu_mode", "ro.boot.redroid_gpu_node", "ro.boot.use_redroid_c2", "ro.boot.use_memfd",
	"ro.boot.redroid_net_ndns", "ro.boot.redroid_net_dns1",
}

// hideBootProps 开机完成后让手机里的小助手把 bootOnlyProps 藏起来（小助手不在就算了）。
func (s *Service) hideBootProps(ctx context.Context, id string) {
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	if err != nil || info.State.Pid <= 0 {
		return
	}
	ns := fmt.Sprintf("/proc/%d/ns/net", info.State.Pid)
	for _, p := range bootOnlyProps {
		if err := touch.Send(ns, "prop hide "+p, 3*time.Second); err != nil {
			return // 老镜像 / 小助手没起来：不藏，照常用
		}
	}
}

// hardwareName 这台手机该用的硬件名：镜像标签 prism.hw 里列着镜像带了驱动的芯片名（小写，如 mt6895）。
// 镜像没处理过 redroid 名字（老镜像，没有标签）返回空，照旧用 redroid。
func (s *Service) hardwareName(ctx context.Context, image string, byKey map[string]any) string {
	out, err := s.run.Run(ctx, "docker", "image", "inspect", "-f", `{{index .Config.Labels "prism.hw"}}`, image)
	if err != nil {
		return ""
	}
	return pickHardware(strings.Fields(out), byKey)
}

var socPrefix = regexp.MustCompile(`^[a-z]+[0-9]+`)

// pickHardware 从镜像带的硬件名里挑一个：芯片型号（MT6895）→ 去掉后缀的芯片型号（真机 ro.soc.model 常带后缀，
// 如 vivo 的 MT6895Z/TCZA）→ 硬件平台名（boardPlatform）。都对不上就用镜像里的第一个：
// 处理过的镜像里已经没有 redroid 的显示合成模块，退回 redroid 会开不了机（2026-11-24 导入魔云腾机型时踩到）。
func pickHardware(have []string, byKey map[string]any) string {
	if len(have) == 0 {
		return ""
	}
	low := func(k string) string { v, _ := byKey[k].(string); return strings.ToLower(strings.TrimSpace(v)) }
	soc := low("socModel")
	for _, c := range []string{soc, socPrefix.FindString(soc), low("boardPlatform")} {
		for _, h := range have {
			if c != "" && h == c {
				return h
			}
		}
	}
	return have[0]
}

// imageEntrypoint 镜像原来的入口（没有返回 nil）。
func (s *Service) imageEntrypoint(ctx context.Context, image string) []string {
	ii, ok := s.docker.(imageInspector)
	if !ok {
		return nil
	}
	ep, err := ii.ImageEntrypoint(ctx, image)
	if err != nil {
		return nil
	}
	return ep
}

// withHardware 把入口里的 androidboot.hardware=redroid 换成芯片名。entrypoint 为空（没用启动器）时改镜像原入口。
func withHardware(entrypoint, imageEP []string, hw string) []string {
	ep := entrypoint
	if ep == nil {
		ep = imageEP
	}
	if ep == nil {
		return entrypoint
	}
	out := append([]string{}, ep...)
	changed := false
	for i, a := range out {
		if strings.HasPrefix(a, "androidboot.hardware=") {
			out[i], changed = "androidboot.hardware="+hw, true
		}
	}
	if !changed {
		return entrypoint
	}
	return out
}
