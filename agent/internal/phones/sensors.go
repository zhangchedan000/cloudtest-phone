package phones

import (
	"context"
	"fmt"
	"hash/fnv"
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
func simSpec(p store.Phone, st store.State, byKey map[string]any) *modem.Spec {
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
	tac, _ := byKey["imeiTac"].(string)
	spec := modem.Pick(country, tac, int64(h.Sum64()>>1))
	return &spec
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
	switch req.Kind {
	case "tap":
		return touch.Send(ns, fmt.Sprintf("tap %d %d", req.X, req.Y), 3*time.Second)
	case "swipe":
		d := req.Duration
		if d <= 0 {
			d = 300
		}
		return touch.Send(ns, fmt.Sprintf("swipe %d %d %d %d %d", req.X, req.Y, req.X2, req.Y2, d), time.Duration(d)*time.Millisecond+3*time.Second)
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

// hardwareName 这台手机该用的硬件名：镜像标签 prism.hw 里有机型芯片名（小写，如 mt6895）就用它；
// 镜像没处理过 redroid 名字（老镜像）返回空，照旧用 redroid（不然找不到显卡驱动开不了机）。
func (s *Service) hardwareName(ctx context.Context, image string, byKey map[string]any) string {
	soc, _ := byKey["socModel"].(string)
	soc = strings.ToLower(strings.TrimSpace(soc))
	if soc == "" {
		return ""
	}
	out, err := s.run.Run(ctx, "docker", "image", "inspect", "-f", `{{index .Config.Labels "prism.hw"}}`, image)
	if err != nil {
		return ""
	}
	for _, h := range strings.Fields(out) {
		if h == soc {
			return soc
		}
	}
	return ""
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
