package phones

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/sensors"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
	"github.com/zhangchedan000/prism-phone/agent/internal/touch"
)

// 传感器（踩坑 #59）：镜像带传感器 HAL（标签 prism.sensors=1）时，pp-init 在安卓开机前起一个喂数据的小程序，
// 扮演模拟器给 HAL 发数据（internal/sensors）。数据按这台手机的种子、代理出口地区（地磁）和时区（室内光线）生成。

func (s *Service) imageHasSensors(ctx context.Context, image string) bool {
	out, err := s.run.Run(ctx, "docker", "image", "inspect", "-f", `{{index .Config.Labels "prism.sensors"}}`, image)
	return err == nil && strings.TrimSpace(out) == "1"
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
