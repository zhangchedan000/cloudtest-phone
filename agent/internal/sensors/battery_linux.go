package sensors

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// 电池的 sysfs（踩坑 #78）：容器里 /sys/class/power_supply 是空的，安卓的电池驱动（health HAL）读不到电池，
// 电池类型（BatteryManager EXTRA_TECHNOLOGY）是空的、健康 / 电压拿不到；App 直接列这个目录也是空的——真机都有。
// 开机前（pp-init 里、安卓 init 之前）在这里挂一个 tmpfs，造出 battery / usb / ac 三个目录和常见的文件。
// 电量、温度后面管家每几分钟漂移一次时一起改（phones/battery.go），和 dumpsys battery set 的值一致。

// PowerSupplyDir 测试时换掉。
var PowerSupplyDir = "/sys/class/power_supply"

// Battery 起始电池状态（管家按机型档案的随机值给）。
type Battery struct {
	Level     int    `json:"level"`
	TempDeci  int    `json:"temp"`   // 0.1℃
	Charging  string `json:"charge"` // ac / usb / none
	Tech      string `json:"tech,omitempty"`
	DesignUAh int    `json:"design,omitempty"` // 设计容量（微安时）
}

// BatteryStatus 充电状态写进 status 文件的字样。
func BatteryStatus(level int, charging string) string {
	switch {
	case charging != "ac" && charging != "usb":
		return "Discharging"
	case level >= 100:
		return "Full"
	}
	return "Charging"
}

// SetupPowerSupply 挂 tmpfs 并写电池文件。出错只返回错误，不影响开机。
func SetupPowerSupply(b Battery) error {
	if err := os.MkdirAll(PowerSupplyDir, 0o755); err != nil {
		return err
	}
	if PowerSupplyDir == "/sys/class/power_supply" {
		if err := syscall.Mount("tmpfs", PowerSupplyDir, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, "mode=755,size=64k"); err != nil {
			return fmt.Errorf("挂 tmpfs：%w", err)
		}
	}
	tech := b.Tech
	if tech == "" {
		tech = "Li-poly"
	}
	design := b.DesignUAh
	if design <= 0 {
		design = 5000000
	}
	online := func(kind string) string {
		if b.Charging == kind {
			return "1"
		}
		return "0"
	}
	// 电压按电量大致估（3.5V~4.4V）
	volt := 3500000 + b.Level*9000
	files := map[string]map[string]string{
		"battery": {
			"type": "Battery", "present": "1", "status": BatteryStatus(b.Level, b.Charging), "health": "Good",
			"technology": tech, "capacity": fmt.Sprint(b.Level), "temp": fmt.Sprint(b.TempDeci),
			"voltage_now": fmt.Sprint(volt), "current_now": "-310000", "charge_full_design": fmt.Sprint(design),
			"charge_full": fmt.Sprint(design * 96 / 100), "charge_counter": fmt.Sprint(design * b.Level / 100), "cycle_count": "37",
		},
		"usb": {"type": "USB", "online": online("usb"), "present": "1", "voltage_max": "5000000", "current_max": "500000"},
		"ac":  {"type": "Mains", "online": online("ac"), "present": "1"},
	}
	for dir, kv := range files {
		d := filepath.Join(PowerSupplyDir, dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		for k, v := range kv {
			// 电池驱动要能改（root 写），App 只读
			if err := os.WriteFile(filepath.Join(d, k), []byte(v+"\n"), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
