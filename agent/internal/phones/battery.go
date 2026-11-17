package phones

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/sensors"
)

// 电池漂移：真机电池一直在变，固定一个值反而是破绽。
// 开机时按机型档案的随机值播种（applyBattery），之后后台每隔几分钟推一下：
// 充电的往上爬、用电的往下掉，温度小幅抖动，每台各走各的。
// dumpsys battery set 会把电池冻在设定值，所以要靠这个循环持续更新。

type batteryState struct {
	Level    int    // 电量 %
	TempDeci int    // 温度，0.1℃
	Charging string // ac / usb / none
}

// seedBattery 开机后记下这台的起始电池状态（精确用档案值，之后再漂移）。
func (s *Service) seedBattery(id string, level, tempDeci int, charging string) {
	s.batt.put(id, batteryState{Level: level, TempDeci: tempDeci, Charging: charging})
}

// driftBattery 把一台运行中手机的电池推一小步并写进容器。没在运行就清掉状态。
func (s *Service) driftBattery(ctx context.Context, id string) {
	if s.State(ctx, id) != "running" {
		s.batt.drop(id)
		return
	}
	cur := s.batt.get(id)
	if cur == nil {
		// 没有内存状态：要么这台没电池字段，要么 agent 刚重启丢了状态。
		// 后者要自愈——按档案重新播种并应用一次，下一轮再漂移（agent 重启不会重跑开机设置）。
		if v := s.batteryValues(id); len(v) > 0 {
			s.applyBattery(ctx, id, containerName(id), v)
		}
		return
	}
	next := *cur
	switch cur.Charging {
	case "ac", "usb":
		next.Level += randInt(0, 2) // 充电：慢慢往上
		if next.Level > 100 {
			next.Level = 100
		}
	default: // none：用电，慢慢往下
		next.Level -= randInt(0, 1)
		if next.Level < 5 {
			next.Level = 5
		}
	}
	next.TempDeci += randInt(-3, 3) // ±0.3℃ 抖动
	if next.TempDeci < 230 {
		next.TempDeci = 230
	}
	if next.TempDeci > 430 {
		next.TempDeci = 430
	}
	name := containerName(id)
	_, _ = s.run.Run(ctx, "docker", "exec", name, "dumpsys", "battery", "set", "level", strconv.Itoa(next.Level))
	_, _ = s.run.Run(ctx, "docker", "exec", name, "dumpsys", "battery", "set", "temp", strconv.Itoa(next.TempDeci))
	s.syncBatteryFiles(ctx, name, next)
	s.batt.put(id, next)
}

// syncBatteryFiles 电池 sysfs（小助手开机前造的，踩坑 #78）跟着改，App 直接读文件和读系统接口一致。老手机没有这个目录就跳过。
func (s *Service) syncBatteryFiles(ctx context.Context, name string, b batteryState) {
	cmd := fmt.Sprintf("d=/sys/class/power_supply/battery; [ -f $d/capacity ] && echo %d > $d/capacity && echo %d > $d/temp && echo %s > $d/status",
		b.Level, b.TempDeci, sensors.BatteryStatus(b.Level, b.Charging))
	_, _ = s.run.Run(ctx, "docker", "exec", name, "sh", "-c", cmd)
}

// batterySeed 机型档案里的起始电量和温度（0.1℃）；没填用 50% / 35℃，电量至少 1（0 且没插电安卓会马上关机，踩坑 #47）。
func batterySeed(v map[string]any) (level, tempDeci int) {
	level, tempDeci = 50, 350
	if lv, ok := toInt(v["batteryLevel"]); ok && lv >= 0 && lv <= 100 {
		level = lv
	}
	if t, ok := toFloat(v["batteryTemp"]); ok && t > -50 && t < 150 {
		tempDeci = int(t * 10)
	}
	if level < 1 {
		level = 1
	}
	return level, tempDeci
}

// batteryValues 从档案里取这台手机的电池字段值（agent 重启后重新播种用）。
func (s *Service) batteryValues(id string) map[string]any {
	st := s.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil {
		return nil
	}
	v := map[string]any{}
	for _, r := range s.resolve(*p, st) {
		if f, ok := s.cat.Field(r.Key); ok && f.Sink.Kind == "battery" && r.Value != nil {
			v[r.Key] = r.Value
		}
	}
	return v
}

// SampleBattery 把所有有电池状态的手机各推一步。
func (s *Service) SampleBattery(ctx context.Context) {
	for _, p := range s.store.Snapshot().Phones {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		s.driftBattery(c, p.ID)
		cancel()
	}
}

// RunBatteryDrift 一直漂移，直到 ctx 结束。
func (s *Service) RunBatteryDrift(ctx context.Context, every time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
			s.SampleBattery(ctx)
		}
	}
}
