package sensors

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// 热区的 sysfs（踩坑 #86）：容器里 /sys/class/thermal 是空的（0 个热区），真机一堆（CPU/GPU/电池/skin），
// App 列这个目录空着一眼就是模拟器。开机前（pp-init 里、安卓 init 之前）挂 tmpfs，造出一组 thermal_zoneN。
// 温度每台按种子不同、开机间稳定（phones/sensors.go 按电池温度附近生成）。

// ThermalDir 测试时换掉。
var ThermalDir = "/sys/class/thermal"

// SetupThermal 挂 tmpfs 并写热区文件。出错只返回错误，不影响开机。
func SetupThermal(zones []ThermalZone) error {
	if len(zones) == 0 {
		return nil
	}
	if err := os.MkdirAll(ThermalDir, 0o755); err != nil {
		return err
	}
	if ThermalDir == "/sys/class/thermal" {
		if err := syscall.Mount("tmpfs", ThermalDir, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, "mode=755,size=1m"); err != nil {
			return fmt.Errorf("挂 tmpfs：%w", err)
		}
	}
	for i, z := range zones {
		d := filepath.Join(ThermalDir, fmt.Sprintf("thermal_zone%d", i))
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		files := map[string]string{
			"type": z.Type, "temp": fmt.Sprint(z.MilliC), "mode": "enabled",
			"policy": "step_wise", "available_policies": "step_wise user_space",
		}
		for k, v := range files {
			if err := os.WriteFile(filepath.Join(d, k), []byte(v+"\n"), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
