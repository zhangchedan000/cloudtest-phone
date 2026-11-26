package phones

import (
	"bytes"
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/sensors"
)

func TestSensorMaskFollowsModel(t *testing.T) {
	// 录了 加速度/磁力计/距离/气压计/计步：气压计保留，计步(19)给不了丢掉；兜底补上缺的 陀螺仪+光线（#166）。
	m := &Model{Sensors: []SensorInfo{{Type: 1}, {Type: 2}, {Type: 8}, {Type: 6}, {Type: 19}}}
	mask := sensorMask(m)
	want := uint32(1<<sensors.Accel | 1<<sensors.Mag | 1<<sensors.Prox | 1<<sensors.Pressure | 1<<sensors.Gyro | 1<<sensors.Light)
	if mask != want {
		t.Fatalf("掩码 %b，应 %b（气压计保留、计步给不了；兜底补 陀螺仪+光线）", mask, want)
	}
	if sensorMask(&Model{}) != sensors.DefaultMask {
		t.Fatal("没录清单用默认")
	}
	f := sensorFeatures(mask)
	// 兜底后一定有基础五件套 + 录了的气压计。
	if !f["android.hardware.sensor.barometer"] || !f["android.hardware.sensor.gyroscope"] || !f["android.hardware.sensor.compass"] {
		t.Fatalf("特性不对：%v", f)
	}
}

func TestBaseSensorFallback(t *testing.T) {
	// 稀档（只有 加速度/光线/距离）：补上 陀螺仪+磁力计，凑够让安卓生成融合传感器的基础集。
	m := &Model{Sensors: []SensorInfo{{Type: 1, Vendor: "MiraMEMS"}, {Type: 5}, {Type: 8}}}
	got := map[int]string{}
	for _, x := range baseSensorFallback(pickSensors(m.Sensors), m) {
		got[x.Type] = x.Vendor
	}
	for _, typ := range []int{1, 2, 4, 5, 8} { // 加速度/磁力计/陀螺仪/光线/距离 都得有
		if _, ok := got[typ]; !ok {
			t.Fatalf("兜底后缺类型 %d：%v", typ, got)
		}
	}
	if got[4] != "MiraMEMS" { // 补的用机型已有传感器的厂商
		t.Fatalf("补的陀螺仪厂商应照机型来：%q", got[4])
	}
	// 满配机型不加重复：录了全套就原样。
	full := &Model{Sensors: []SensorInfo{{Type: 1}, {Type: 2}, {Type: 4}, {Type: 5}, {Type: 8}}}
	if n := len(baseSensorFallback(pickSensors(full.Sensors), full)); n != 5 {
		t.Fatalf("满配不该加重复，应 5 个，得 %d", n)
	}
}

func TestPickSensors(t *testing.T) {
	list := []SensorInfo{
		{Type: 1, Name: "acc wakeup", Flags: 1}, {Type: 1, Name: "acc", Flags: 0},
		{Type: 8, Name: "prox nonwake", Flags: 2}, {Type: 8, Name: "prox", Flags: 3},
		{Type: 19, Name: "step"},
	}
	got := map[int]string{}
	for _, x := range pickSensors(list) {
		got[x.Type] = x.Name
	}
	if got[1] != "acc" || got[8] != "prox" || len(got) != 2 {
		t.Fatalf("挑错了：%v", got)
	}
}

func TestSensorShimFile(t *testing.T) {
	shim := append([]byte("ELF......PPSNCFG1"), make([]byte, 20000)...)
	inner := []byte("\x7fELFinner")
	out, err := sensorShimFile(shim, inner, []SensorInfo{{Type: 1, Name: "icm|42631", Vendor: "iven", Version: 1, MaxRange: 78.4, MinDelay: 5000}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out)%4096 != len(inner) || !bytes.HasSuffix(out, inner) {
		t.Fatalf("真库要接在页边界：%d", len(out))
	}
	cfg := string(out[9+8+1 : 9+8+200])
	if !strings.Contains(cfg, "o=20480\ns=1|icm 42631|iven|1|78.4|0|0|5000|0|0|0\n") || out[9+8] != 1 {
		t.Fatalf("配置区不对：%q", cfg)
	}
}
