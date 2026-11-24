package phones

import (
	"bytes"
	"strings"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/sensors"
)

func TestSensorMaskFollowsModel(t *testing.T) {
	m := &Model{Sensors: []SensorInfo{{Type: 1}, {Type: 2}, {Type: 8}, {Type: 6}, {Type: 19}}}
	mask := sensorMask(m)
	want := uint32(1<<sensors.Accel | 1<<sensors.Mag | 1<<sensors.Prox | 1<<sensors.Pressure)
	if mask != want {
		t.Fatalf("掩码 %b，应 %b（没有陀螺仪、光线、未校准版；计步 19 我们给不了）", mask, want)
	}
	if sensorMask(&Model{}) != sensors.DefaultMask {
		t.Fatal("没录清单用默认")
	}
	f := sensorFeatures(mask)
	if !f["android.hardware.sensor.barometer"] || f["android.hardware.sensor.gyroscope"] {
		t.Fatalf("特性不对：%v", f)
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
