package phones

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
	"github.com/zhangchedan000/prism-phone/agent/internal/wifi"
)

// 虚拟 WiFi（踩坑 #57，宿主机那边的细节见 internal/wifi）：
// 镜像带 WiFi HAL（host/build-wifi-image.sh 构建，标签 prism.wifi=1）且宿主机能做虚拟 WiFi 时，
// 手机开机后马上（安卓的有线网管理启动之前）把 eth0 换成 WiFi：手机里只剩 wlan0，安卓看到的是 WIFI 网络。
// 镜像不带 WiFi 或宿主机不支持，就照旧用 eth0。

// imageHasWifi 镜像带 WiFi HAL 吗（看标签）。
func (s *Service) imageHasWifi(ctx context.Context, image string) bool {
	out, err := s.run.Run(ctx, "docker", "image", "inspect", "-f", `{{index .Config.Labels "prism.wifi"}}`, image)
	return err == nil && strings.TrimSpace(out) == "1"
}

// wifiPlan 这台手机的 WiFi 配置（MAC 用机型档案的 wifiMac 字段）。
func (s *Service) wifiPlan(id string, byKey map[string]any) (wifi.Plan, error) {
	// 路由器按代理出口国家选当地常见的牌子（没检测出国家用默认那套）
	country := ""
	st := s.store.Snapshot()
	if p, err := st.Phone(id); err == nil {
		if px := st.Proxy(p.ProxyID); px != nil && px.Check != nil {
			country = px.Check.Country
		}
	}
	return wifi.PlanForCountry(id, str(byKey["wifiMac"]), s.cfg.DataDir, country)
}

// setupWifi 容器启动后马上调用。返回 true = 这台手机现在走 WiFi。失败时清掉做了一半的，手机照旧用 eth0。
func (s *Service) setupWifi(ctx context.Context, id, image string, byKey map[string]any) bool {
	if !s.cfg.Wifi || !s.imageHasWifi(ctx, image) {
		return false
	}
	plan, err := s.wifiPlan(id, byKey)
	if err != nil {
		log.Printf("手机 %s：WiFi 没配（照旧用有线）：%v", id, err)
		return false
	}
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	if err != nil || info.State.Pid <= 0 {
		log.Printf("手机 %s：WiFi 没配（拿不到容器进程）：%v", id, err)
		return false
	}
	dns := s.cfg.WifiDNS
	if dns == "" {
		dns = s.gateway() + ":53"
	}
	if err := wifi.Setup(ctx, s.run, s.newRadio, plan, info.State.Pid, s.gateway(), dns); err != nil {
		log.Printf("手机 %s：WiFi 没配好（照旧用有线）：%v", id, err)
		_ = wifi.Teardown(ctx, s.run, plan)
		return false
	}
	return true
}

// teardownWifi 关机 / 删除 / 重置后清掉路由器（没有就什么都不做）。
func (s *Service) teardownWifi(ctx context.Context, id string) {
	// 只需要名字（不依赖 wifiMac 字段：字段改过也能清干净）
	p, err := wifi.PlanFor(id, "02:00:00:00:00:00", s.cfg.DataDir)
	if err != nil {
		return
	}
	if err := wifi.Teardown(ctx, s.run, p); err != nil {
		log.Printf("手机 %s：清理 WiFi 路由器：%v", id, err)
	}
}

// EnsureWifi 所有走 WiFi 的运行中手机：热点 / DHCP 没在跑就补起来（上网检查前顺带做）。
func (s *Service) EnsureWifi(ctx context.Context) {
	if !s.cfg.Wifi {
		return
	}
	for _, p := range s.store.Snapshot().Phones {
		if s.wifiActive(p.ID) && s.isRunning(ctx, p.ID) {
			s.ensureWifi(ctx, p.ID)
		}
	}
}

// ensureWifi 一台手机的热点 / DHCP 没在跑就重新拉起来（手机会自己重连）。
func (s *Service) ensureWifi(ctx context.Context, id string) {
	if !s.wifiActive(id) {
		return
	}
	// dnsmasq 的固定地址要手机的真 MAC：从这台的配置里取
	st := s.store.Snapshot()
	var p wifi.Plan
	for _, ph := range st.Phones {
		if ph.ID == id {
			var err error
			if p, err = s.wifiPlan(id, s.resolvedValues(ph, st)); err != nil {
				return
			}
		}
	}
	if p.Netns == "" {
		return
	}
	if fixed, err := wifi.EnsureDaemons(ctx, s.run, p); err != nil {
		log.Printf("手机 %s：WiFi 热点补起来失败：%v", id, err)
	} else if fixed {
		log.Printf("手机 %s：WiFi 热点没在跑，已重新拉起", id)
	}
}

// wifiActive 这台手机现在走 WiFi 吗（路由器命名空间在）。
func (s *Service) wifiActive(id string) bool {
	_, err := os.Stat("/run/netns/ppr-" + id)
	return err == nil
}

// connectWifi 开机设置里调用：打开 WiFi、连上这台手机自己的路由器（已连上就不动）。
// 安卓会把 WiFi 配置存在 /data 里，以后开机自动连。
func (s *Service) connectWifi(ctx context.Context, id string, byKey map[string]any) error {
	if !s.wifiActive(id) {
		return nil
	}
	plan, err := s.wifiPlan(id, byKey)
	if err != nil {
		return err
	}
	name := containerName(id)
	_, _ = s.run.Run(ctx, "docker", "exec", name, "cmd", "wifi", "set-wifi-enabled", "enabled")
	if st, _ := s.run.Run(ctx, "docker", "exec", name, "cmd", "wifi", "status"); strings.Contains(st, fmt.Sprintf("connected to %q", plan.SSID)) {
		return nil
	}
	if out, err := s.run.Run(ctx, "docker", "exec", name, "cmd", "wifi", "connect-network", plan.SSID, "wpa2", plan.Passphrase); err != nil {
		return fmt.Errorf("连 WiFi 失败：%v %s", err, out)
	}
	return nil
}

// resolvedValues 字段键 → 最终值。
func (s *Service) resolvedValues(p store.Phone, st store.State) map[string]any {
	m := map[string]any{}
	for _, r := range s.resolve(p, st) {
		m[r.Key] = r.Value
	}
	return m
}
