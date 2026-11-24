package phones

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhangchedan000/prism-phone/agent/internal/sensors"
)

// 传感器清单照真机（2026-11-24，学魔云腾：它的机型包录了真机传感器的名字、厂商、量程……，云机照着报）。
// 以前所有手机都是同一张表：Accelerometer / Gyroscope / Magnetometer……厂商全是 MTK，量程是模拟器的值。
// 现在：
//   - 有哪几种跟真机（真机没有陀螺仪未校准版就不开；有气压计就开）：喂数据的掩码按机型算（sensorMask）；
//   - 名字、厂商、版本、量程、精度、功耗、频率范围、FIFO 照真机：传感器转接库 host/image/sensorshim 挂在
//     镜像的传感器子 HAL 文件名上，文件后面接着原来的库，加载后改它的传感器表（不多文件、不改 hals.conf）；
//   - 系统特性里的传感器项跟着实际有的报（features.go）。
// 改不了的：真机一般有 30~50 个传感器（计步、抬手、口袋模式……），我们只能有模拟器 HAL 支持的这几种。

// SensorInfo 真机一个传感器（机型包 sensor.json / dumpsys sensorservice）。量程等为 0 表示没录，用 HAL 原来的。
type SensorInfo struct {
	Type       int     `json:"type"`
	Name       string  `json:"name"`
	Vendor     string  `json:"vendor"`
	Version    int     `json:"version"`
	MaxRange   float64 `json:"maxRange"`
	Resolution float64 `json:"resolution"`
	Power      float64 `json:"power"`
	MinDelay   int     `json:"minDelay"`
	MaxDelay   int     `json:"maxDelay"`
	FifoRes    int     `json:"fifoRes"`
	FifoMax    int     `json:"fifoMax"`
	Flags      int     `json:"flags"`
}

// 安卓传感器类型 → 我们喂数据的编号（模拟器协议的掩码位）、系统特性名
var (
	sensorTypeBit = map[int]int{1: sensors.Accel, 2: sensors.Mag, 4: sensors.Gyro, 5: sensors.Light, 6: sensors.Pressure,
		8: sensors.Prox, 14: sensors.MagUncal, 16: sensors.GyroUncal, 35: sensors.AccelUncal}
	sensorBitFeature = map[int]string{sensors.Accel: "android.hardware.sensor.accelerometer", sensors.Gyro: "android.hardware.sensor.gyroscope",
		sensors.Mag: "android.hardware.sensor.compass", sensors.Light: "android.hardware.sensor.light",
		sensors.Pressure: "android.hardware.sensor.barometer", sensors.Prox: "android.hardware.sensor.proximity"}
)

// sensorMask 这款机型开哪几种传感器：真机有的里我们能喂数据的（加速度总开着）；没录清单的用默认那套。
func sensorMask(m *Model) uint32 {
	if m == nil || len(m.Sensors) == 0 {
		return sensors.DefaultMask
	}
	mask := uint32(1) << sensors.Accel
	for _, x := range m.Sensors {
		if b, ok := sensorTypeBit[x.Type]; ok {
			mask |= 1 << b
		}
	}
	return mask
}

// sensorFeatures 掩码对应的系统特性。
func sensorFeatures(mask uint32) map[string]bool {
	out := map[string]bool{}
	for b, f := range sensorBitFeature {
		if mask&(1<<b) != 0 {
			out[f] = true
		}
	}
	return out
}

// pickSensors 每种类型挑真机的一个：一般挑不唤醒的（真机同一种常有唤醒 / 不唤醒两个），距离挑唤醒的。
func pickSensors(list []SensorInfo) []SensorInfo {
	var out []SensorInfo
	for t := range sensorTypeBit {
		var best *SensorInfo
		for i := range list {
			x := &list[i]
			if x.Type != t {
				continue
			}
			wake := x.Flags&1 != 0
			if best == nil || (t == 8 && wake && best.Flags&1 == 0) || (t != 8 && !wake && best.Flags&1 != 0) {
				best = x
			}
		}
		if best != nil {
			out = append(out, *best)
		}
	}
	return out
}

func cfgText(s string) string {
	return strings.NewReplacer("|", " ", "\n", " ", "\x00", "").Replace(s)
}

// sensorShimFile 转接库（配置区写好）+ 补到页边界 + 原来的子 HAL 库。
func sensorShimFile(shim, inner []byte, picks []SensorInfo) ([]byte, error) {
	const size = 16384
	i := bytes.Index(shim, []byte("PPSNCFG1"))
	if i < 0 || i+size > len(shim) {
		return nil, fmt.Errorf("传感器转接库里找不到配置区")
	}
	off := (len(shim) + 4095) / 4096 * 4096
	var b strings.Builder
	fmt.Fprintf(&b, "o=%d\n", off)
	for _, x := range picks {
		fmt.Fprintf(&b, "s=%d|%s|%s|%d|%g|%g|%g|%d|%d|%d|%d\n", x.Type, cfgText(x.Name), cfgText(x.Vendor), x.Version,
			x.MaxRange, x.Resolution, x.Power, x.MinDelay, x.MaxDelay, x.FifoRes, x.FifoMax)
	}
	if b.Len() > size-10 {
		return nil, fmt.Errorf("传感器配置太长")
	}
	out := make([]byte, off, off+len(inner))
	copy(out, shim)
	area := out[i+8 : i+size]
	for k := range area {
		area[k] = 0
	}
	area[0] = 1
	copy(area[1:], b.String())
	return append(out, inner...), nil
}

// sensorShimBinds 机型录了传感器清单、镜像带传感器 HAL 时，开机前挂的转接库。
func (s *Service) sensorShimBinds(ctx context.Context, id, image string, m *Model) []string {
	if m == nil || len(m.Sensors) == 0 || s.cfg.Tools == nil || !s.cfg.Sensors || !s.imageHasSensors(ctx, image) {
		return nil
	}
	shim, err := fs.ReadFile(s.cfg.Tools, "libsensors_pp.so")
	if err != nil {
		return nil
	}
	conf, err := s.hwModules(ctx, image, map[string]string{"etc/sensors-hals.conf": "/vendor/etc/sensors/hals.conf"})
	if err != nil {
		log.Printf("手机 %s：读镜像传感器配置失败，传感器照旧：%v", id, err)
		return nil
	}
	raw, _ := os.ReadFile(conf["etc/sensors-hals.conf"])
	name := ""
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			name = filepath.Base(l)
			break
		}
	}
	if name == "" || !strings.HasSuffix(name, ".so") {
		return nil
	}
	target := "/vendor/lib64/hw/" + name
	lib, err := s.hwModules(ctx, image, map[string]string{"lib64/hw/sensors-subhal": target})
	if err != nil {
		log.Printf("手机 %s：取传感器子 HAL 失败，传感器照旧：%v", id, err)
		return nil
	}
	inner, err := os.ReadFile(lib["lib64/hw/sensors-subhal"])
	if err != nil {
		return nil
	}
	out, err := sensorShimFile(shim, inner, pickSensors(m.Sensors))
	if err != nil {
		log.Printf("手机 %s：%v", id, err)
		return nil
	}
	dir := filepath.Join(s.phoneDir(id), "sensors")
	_ = os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, out, 0o644); err != nil {
		return nil
	}
	return []string{p + ":" + target + ":ro"}
}
