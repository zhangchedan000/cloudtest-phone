// Package sensors 给云手机喂传感器数据（踩坑 #59）。
//
// 手机镜像里装的是谷歌安卓模拟器的传感器 HAL（host/build-wifi-image.sh 加进去，并打补丁让它走管道文件），
// 它从 /dev/goldfish_pipe_dprctd 读模拟器发来的数据。我们在容器里开一个伪终端（pty），
// 从端绑到这个路径，主端由这里的程序扮演模拟器：回答有哪些传感器、按 HAL 要的频率发数据。
//
// 协议（device/generic/goldfish/hals/sensors）：每条消息 = 4 位十六进制长度 + 内容。
//
//	HAL → 我们：先写 "pipe:qemud:sensors\0"（不带长度头），之后 "time:<ns>"、"list-sensors"、
//	           "set:<名字>:<0|1>"、"set-delay:<毫秒>"
//	我们 → HAL：list-sensors 回十进制掩码；之后 "acceleration:x:y:z"、"magnetic:x:y:z"、"light:v" ……
//
// 数据模型：手机平放在桌上（略有倾斜），有 MEMS 传感器正常的噪声和零偏，地磁按地区，光线按当地时间的室内亮度。
// 每台手机的倾斜、朝向、零偏由种子决定，各不相同但开机之间稳定。
package sensors

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/modem"
)

// 传感器在模拟器协议里的编号（掩码的位）和名字（set: 命令用的）。
const (
	Accel      = 0
	Gyro       = 1
	Mag        = 2
	Orient     = 3
	Temp       = 4
	Prox       = 5
	Light      = 6
	Pressure   = 7
	Humidity   = 8
	MagUncal   = 9
	GyroUncal  = 10
	AccelUncal = 17
)

// setNames 编号 → set: 命令里的名字。
var setNames = map[int]string{
	Accel: "acceleration", Gyro: "gyroscope", Mag: "magnetic-field", Orient: "orientation",
	Temp: "temperature", Prox: "proximity", Light: "light", Pressure: "pressure", Humidity: "humidity",
	MagUncal: "magnetic-field-uncalibrated", GyroUncal: "gyroscope-uncalibrated", AccelUncal: "acceleration-uncalibrated",
}

// DefaultMask 一般手机都有的：加速度、陀螺仪、磁力计（及三者的未校准版）、距离、光线。
const DefaultMask = 1<<Accel | 1<<Gyro | 1<<Mag | 1<<Prox | 1<<Light | 1<<MagUncal | 1<<GyroUncal | 1<<AccelUncal

// Spec 一台手机的传感器配置（管家开机时算好，经 pp-init 传进容器）。
type Spec struct {
	Mask     uint32     `json:"mask"`
	Seed     int64      `json:"seed"`
	MagWorld [3]float64 `json:"mag"`      // 当地地磁（微特斯拉）：北、东、下
	TZOffset int        `json:"tzOffset"` // 当地时区相对 UTC 的秒数（光线按当地时间）
	// 虚拟触摸屏（internal/touch，同一个小助手进程负责）：屏幕像素和触控芯片名；W 为 0 不建
	W         int    `json:"w,omitempty"`
	H         int    `json:"h,omitempty"`
	TouchName string `json:"touch,omitempty"`
	// 基带和 SIM 卡（internal/modem，同一个小助手进程负责）；空就不模拟
	Modem *modem.Spec `json:"modem,omitempty"`
}

// Encode / Decode 命令行参数格式。
func (s Spec) Encode() string { b, _ := json.Marshal(s); return string(b) }

func Decode(v string) (Spec, error) {
	var s Spec
	err := json.Unmarshal([]byte(v), &s)
	if s.Mask == 0 {
		s.Mask = DefaultMask
	}
	return s, err
}

// MagForRegion 大致的地磁（北、东、下，微特斯拉），按代理出口地区（没有就用香港）。
// 数值取自 WMM 模型的量级，手机上本来就有几微特斯拉的误差，不需要精确。
func MagForRegion(country string) [3]float64 {
	switch strings.ToUpper(country) {
	case "US":
		return [3]float64{20.5, -1.8, 46.0}
	case "GB", "DE", "FR", "NL":
		return [3]float64{19.5, 0.3, 44.5}
	case "JP":
		return [3]float64{30.5, -3.6, 35.0}
	case "SG", "MY", "ID":
		return [3]float64{40.0, 0.2, -10.5}
	case "TW":
		return [3]float64{35.5, -2.4, 26.0}
	default: // HK / 中国南部
		return [3]float64{37.0, -1.6, 27.0}
	}
}

// model 一台手机的「物理状态」。
type model struct {
	mu       sync.Mutex
	rnd      *rand.Rand
	pitch    float64 // 绕 x 轴倾斜（弧度）
	roll     float64 // 绕 y 轴倾斜
	heading  float64 // 屏幕朝上平放时，手机顶部朝哪（弧度，0=北）
	gyroBias [3]float64
	accBias  [3]float64
	magHard  [3]float64 // 硬铁偏移（手机自身磁性部件）
	spec     Spec
	pressure float64
	lux      float64
}

func newModel(spec Spec) *model {
	r := rand.New(rand.NewSource(spec.Seed))
	m := &model{rnd: r, spec: spec}
	m.pitch = (r.Float64()*2 - 1) * 2.5 * math.Pi / 180
	m.roll = (r.Float64()*2 - 1) * 2.5 * math.Pi / 180
	m.heading = r.Float64() * 2 * math.Pi
	for i := range m.gyroBias {
		m.gyroBias[i] = (r.Float64()*2 - 1) * 0.0015
		m.accBias[i] = (r.Float64()*2 - 1) * 0.03
		m.magHard[i] = (r.Float64()*2 - 1) * 8
	}
	m.pressure = 1008 + r.Float64()*8
	return m
}

// rotate 世界坐标（北、东、下）→ 手机坐标（安卓：x 右、y 顶部、z 屏幕朝外）。手机屏幕朝上平放。
func (m *model) rotate(v [3]float64) [3]float64 {
	// 先按朝向转到手机平面：手机 y 轴指向 heading
	n, e, d := v[0], v[1], v[2]
	ch, sh := math.Cos(m.heading), math.Sin(m.heading)
	x := -n*sh + e*ch // 手机右侧
	y := n*ch + e*sh  // 手机顶部
	z := -d           // 屏幕朝上 → z 向上 = -下
	// 再加上小倾斜
	cp, sp := math.Cos(m.pitch), math.Sin(m.pitch)
	y, z = y*cp-z*sp, y*sp+z*cp
	cr, sr := math.Cos(m.roll), math.Sin(m.roll)
	x, z = x*cr+z*sr, -x*sr+z*cr
	return [3]float64{x, y, z}
}

func (m *model) step(dt float64) {
	// 极慢的随机游走：桌面轻微振动、温度漂移
	m.pitch += m.rnd.NormFloat64() * 0.00002 * math.Sqrt(dt)
	m.roll += m.rnd.NormFloat64() * 0.00002 * math.Sqrt(dt)
	m.pressure += m.rnd.NormFloat64() * 0.002 * math.Sqrt(dt)
}

const g = 9.80665

func (m *model) accel() [3]float64 {
	// 加速度计量的是「比力」：静止时 = 向上的 g
	a := m.rotate([3]float64{0, 0, -g})
	for i := range a {
		a[i] += m.accBias[i] + m.rnd.NormFloat64()*0.012
	}
	return a
}

func (m *model) gyro() [3]float64 {
	var w [3]float64
	for i := range w {
		w[i] = m.rnd.NormFloat64() * 0.0012 // 校准后零偏已去掉
	}
	return w
}

func (m *model) gyroUncal() [3]float64 {
	w := m.gyro()
	for i := range w {
		w[i] += m.gyroBias[i]
	}
	return w
}

func (m *model) mag(uncal bool) [3]float64 {
	b := m.rotate(m.spec.MagWorld)
	for i := range b {
		b[i] += m.rnd.NormFloat64() * 0.35
		if uncal {
			b[i] += m.magHard[i]
		}
	}
	return b
}

// luxAt 室内光线：白天 150–400 lux，傍晚变暗，夜里开灯 60–120 或关灯后几 lux。
func (m *model) luxAt(t time.Time) float64 {
	h := float64(t.UTC().Add(time.Duration(m.spec.TZOffset)*time.Second).Hour()) +
		float64(t.Minute())/60
	var base float64
	switch {
	case h >= 8 && h < 17:
		base = 220 + 120*math.Sin((h-8)/9*math.Pi)
	case h >= 17 && h < 23:
		base = 90
	case h >= 7 && h < 8:
		base = 60
	default:
		base = 4
	}
	return math.Max(0, base*(1+m.rnd.NormFloat64()*0.03))
}

// Feeder 跑一个 HAL 连接：rw 是 pty 主端。返回时连接已断。
type Feeder struct {
	m       *model
	w       io.Writer
	wmu     sync.Mutex
	enabled map[int]bool
	delay   time.Duration
	mu      sync.Mutex
	Log     func(string)
}

func NewFeeder(spec Spec, w io.Writer) *Feeder {
	return &Feeder{m: newModel(spec), w: w, enabled: map[int]bool{}, delay: 200 * time.Millisecond, Log: func(string) {}}
}

func (f *Feeder) send(s string) error {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	_, err := fmt.Fprintf(f.w, "%04x%s", len(s), s)
	return err
}

// Reset HAL 重新连上来了：全部先停（等它重新 set）。
func (f *Feeder) Reset() {
	f.mu.Lock()
	f.enabled = map[int]bool{}
	f.delay = 200 * time.Millisecond
	f.mu.Unlock()
}

// Handle 处理 HAL 发来的一条消息。
func (f *Feeder) Handle(msg string) {
	f.Log("HAL> " + msg)
	switch {
	case msg == "list-sensors":
		_ = f.send(strconv.FormatUint(uint64(f.m.spec.Mask), 10))
	case strings.HasPrefix(msg, "set-delay:"):
		if ms, err := strconv.Atoi(strings.TrimPrefix(msg, "set-delay:")); err == nil {
			f.mu.Lock()
			f.delay = time.Duration(max(ms, 5)) * time.Millisecond
			f.mu.Unlock()
		}
	case strings.HasPrefix(msg, "set:"):
		parts := strings.Split(msg, ":")
		if len(parts) != 3 {
			return
		}
		for id, n := range setNames {
			if n == parts[1] {
				f.mu.Lock()
				f.enabled[id] = parts[2] == "1"
				f.mu.Unlock()
				if id == Prox && parts[2] == "1" {
					_ = f.send("proximity:5.0") // 一开就报一次「远」（变化型传感器）
				}
				if id == Light && parts[2] == "1" {
					f.m.lux = f.m.luxAt(time.Now())
					_ = f.send(fmt.Sprintf("light:%.1f", f.m.lux))
				}
			}
		}
	}
}

func vec(name string, v [3]float64) string {
	return fmt.Sprintf("%s:%.5f:%.5f:%.5f", name, v[0], v[1], v[2])
}

// Tick 按当前频率发一轮数据（由 Run 定时调用）。
func (f *Feeder) Tick(now time.Time, dt float64) error {
	f.mu.Lock()
	en := map[int]bool{}
	for k, v := range f.enabled {
		en[k] = v
	}
	f.mu.Unlock()
	m := f.m
	m.mu.Lock()
	defer m.mu.Unlock()
	m.step(dt)
	var msgs []string
	if en[Accel] {
		msgs = append(msgs, vec("acceleration", m.accel()))
	}
	if en[AccelUncal] {
		msgs = append(msgs, vec("acceleration-uncalibrated", m.accel()))
	}
	if en[Gyro] {
		msgs = append(msgs, vec("gyroscope", m.gyro()))
	}
	if en[GyroUncal] {
		msgs = append(msgs, vec("gyroscope-uncalibrated", m.gyroUncal()))
	}
	if en[Mag] {
		msgs = append(msgs, vec("magnetic", m.mag(false)))
	}
	if en[MagUncal] {
		msgs = append(msgs, vec("magnetic-uncalibrated", m.mag(true)))
	}
	if en[Pressure] {
		msgs = append(msgs, fmt.Sprintf("pressure:%.2f", m.pressure+m.rnd.NormFloat64()*0.015))
	}
	// 光线是变化型：变化超过 3% 才报
	if en[Light] {
		if l := m.luxAt(now); math.Abs(l-m.lux) > 0.03*math.Max(m.lux, 3) {
			m.lux = l
			msgs = append(msgs, fmt.Sprintf("light:%.1f", l))
		}
	}
	for _, s := range msgs {
		if err := f.send(s); err != nil {
			return err
		}
	}
	return nil
}

// Delay 当前发数据的间隔。
func (f *Feeder) Delay() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.delay
}

// Frames 把从 HAL 读到的字节拆成消息（处理握手的 "pipe:...\0"）。返回剩下没拆完的。
func Frames(buf []byte, handle func(string)) []byte {
	for {
		if len(buf) >= 5 && string(buf[:5]) == "pipe:" {
			i := strings.IndexByte(string(buf), 0)
			if i < 0 {
				return buf
			}
			handle(string(buf[:i]))
			buf = buf[i+1:]
			continue
		}
		if len(buf) < 4 {
			return buf
		}
		n, err := strconv.ParseUint(string(buf[:4]), 16, 16)
		if err != nil {
			buf = buf[1:] // 乱了：丢一个字节重新对齐
			continue
		}
		if len(buf) < 4+int(n) {
			return buf
		}
		handle(string(buf[4 : 4+n]))
		buf = buf[4+n:]
	}
}
