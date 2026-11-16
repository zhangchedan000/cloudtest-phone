// Package touch 给云手机一块「真的」虚拟触摸屏（2026-11-16）。
//
// 以前手机里没有任何触摸设备，安卓只看到一个虚拟键盘；管理页的点击、滑动是 `input tap/swipe` 注入的，
// 不来自任何设备、按压力度和手指大小永远一样——App 一查输入设备列表或看 MotionEvent 就知道。
//
// 现在：手机里的小助手进程（vendor.sensord，pp-init 开机前起的，见 internal/sensors）用内核的 uinput
// 建一块 10 点触控屏（INPUT_PROP_DIRECT + ABS_MT_*，和真手机的触控芯片一样），在容器的 /dev/input 下建好设备节点，
// 安卓自己发现它、当成内置触摸屏。管理页的点击、滑动发给小助手，它像真手指一样按下去：
// 落点有一两个像素的偏差、按压力度和接触面积每次不同、按住几十毫秒还会轻微挪动；滑动按 120Hz 采样、先快后慢、略带弧度。
package touch

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// 内核 input / uinput 常量（include/uapi/linux/input-event-codes.h、uinput.h）。
const (
	evSyn = 0x00
	evKey = 0x01
	evAbs = 0x03

	synReport = 0

	btnToolFinger = 0x145
	btnTouch      = 0x14a

	absMTSlot       = 0x2f
	absMTTouchMajor = 0x30
	absMTTouchMinor = 0x31
	absMTPositionX  = 0x35
	absMTPositionY  = 0x36
	absMTTrackingID = 0x39
	absMTPressure   = 0x3a

	inputPropDirect = 0x01
	busI2C          = 0x18

	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502
	uiDevSetup   = 0x405c5503 // _IOW('U', 3, struct uinput_setup)，92 字节
	uiAbsSetup   = 0x401c5504 // _IOW('U', 4, struct uinput_abs_setup)，28 字节
	uiSetEvBit   = 0x40045564
	uiSetKeyBit  = 0x40045565
	uiSetAbsBit  = 0x40045567
	uiSetPropBit = 0x4004556e
	uiGetSysname = 0x8040552c // _IOC(_IOC_READ, 'U', 44, 64)

	maxSlots = 10
)

// Config 触摸屏的样子。
type Config struct {
	W, H int    // 屏幕像素（坐标范围 0..W-1 / 0..H-1，和屏幕一一对应）
	Name string // 触控芯片名（真手机上像 fts_ts、NVTCapacitiveTouchScreen）
}

// Device 一块虚拟触摸屏。
type Device struct {
	f    *os.File
	cfg  Config
	mu   sync.Mutex
	rnd  *rand.Rand
	next int32 // 下一个触点编号（tracking id）
	Node string
}

func ioctl(fd uintptr, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

// Create 打开 /dev/uinput 建设备，并在 devDir（容器的 /dev/input）下建好设备节点，安卓才看得见。
func Create(cfg Config, devDir string, seed int64) (*Device, error) {
	if cfg.W <= 0 || cfg.H <= 0 {
		return nil, fmt.Errorf("屏幕大小不对：%dx%d", cfg.W, cfg.H)
	}
	if cfg.Name == "" {
		cfg.Name = "fts_ts"
	}
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("打开 /dev/uinput：%w", err)
	}
	fd := f.Fd()
	fail := func(what string, err error) (*Device, error) {
		f.Close()
		return nil, fmt.Errorf("%s：%w", what, err)
	}
	for _, ev := range []uintptr{evSyn, evKey, evAbs} {
		if err := ioctl(fd, uiSetEvBit, ev); err != nil {
			return fail("设事件类型", err)
		}
	}
	for _, k := range []uintptr{btnTouch, btnToolFinger} {
		if err := ioctl(fd, uiSetKeyBit, k); err != nil {
			return fail("设按键", err)
		}
	}
	if err := ioctl(fd, uiSetPropBit, inputPropDirect); err != nil {
		return fail("设直接触控属性", err)
	}
	axes := []struct {
		code     uint16
		min, max int32
	}{
		{absMTSlot, 0, maxSlots - 1},
		{absMTTrackingID, 0, 65535},
		{absMTPositionX, 0, int32(cfg.W - 1)},
		{absMTPositionY, 0, int32(cfg.H - 1)},
		{absMTTouchMajor, 0, 255},
		{absMTTouchMinor, 0, 255},
		{absMTPressure, 0, 255},
	}
	for _, a := range axes {
		if err := ioctl(fd, uiSetAbsBit, uintptr(a.code)); err != nil {
			return fail("设坐标轴", err)
		}
		// struct uinput_abs_setup { __u16 code; struct input_absinfo { value, min, max, fuzz, flat, resolution } }（中间 2 字节对齐）
		var buf [28]byte
		binary.LittleEndian.PutUint16(buf[0:], a.code)
		binary.LittleEndian.PutUint32(buf[8:], uint32(a.min))
		binary.LittleEndian.PutUint32(buf[12:], uint32(a.max))
		if err := ioctl(fd, uiAbsSetup, uintptr(unsafe.Pointer(&buf[0]))); err != nil {
			return fail("设坐标范围", err)
		}
	}
	// struct uinput_setup { struct input_id { bustype, vendor, product, version }; char name[80]; __u32 ff_effects_max; }
	var setup [92]byte
	binary.LittleEndian.PutUint16(setup[0:], busI2C)
	copy(setup[8:87], cfg.Name)
	if err := ioctl(fd, uiDevSetup, uintptr(unsafe.Pointer(&setup[0]))); err != nil {
		return fail("设设备信息", err)
	}
	if err := ioctl(fd, uiDevCreate, 0); err != nil {
		return fail("建设备", err)
	}
	d := &Device{f: f, cfg: cfg, rnd: rand.New(rand.NewSource(seed)), next: 1}
	node, err := d.makeNode(devDir)
	if err != nil {
		d.Close()
		return nil, err
	}
	d.Node = node
	return d, nil
}

// makeNode 设备节点在宿主机的 /dev 里自动出现，容器的 /dev 是另一份，要自己按主次设备号建。
func (d *Device) makeNode(devDir string) (string, error) {
	var name [64]byte
	if err := ioctl(d.f.Fd(), uiGetSysname, uintptr(unsafe.Pointer(&name[0]))); err != nil {
		return "", fmt.Errorf("读设备名：%w", err)
	}
	sys := filepath.Join("/sys/devices/virtual/input", strings.TrimRight(string(name[:]), "\x00"))
	var ev string
	for i := 0; i < 50 && ev == ""; i++ { // sysfs 里 eventN 出现可能要一点点时间
		if ents, err := os.ReadDir(sys); err == nil {
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), "event") {
					ev = e.Name()
				}
			}
		}
		if ev == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if ev == "" {
		return "", fmt.Errorf("%s 下没有 eventN", sys)
	}
	raw, err := os.ReadFile(filepath.Join(sys, ev, "dev"))
	if err != nil {
		return "", err
	}
	mm := strings.SplitN(strings.TrimSpace(string(raw)), ":", 2)
	maj, _ := strconv.Atoi(mm[0])
	min, _ := strconv.Atoi(mm[len(mm)-1])
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		return "", err
	}
	node := filepath.Join(devDir, ev)
	_ = os.Remove(node)
	dev := uint64(maj)<<8 | uint64(min&0xff) | uint64(min&^0xff)<<12
	if err := syscall.Mknod(node, syscall.S_IFCHR|0o660, int(dev)); err != nil {
		return "", fmt.Errorf("建设备节点 %s：%w", node, err)
	}
	_ = os.Chown(node, 0, 1004) // root:input（安卓的 input 组），system_server 才读得到
	_ = os.Chmod(node, 0o660)
	return node, nil
}

// Close 拆掉设备。
func (d *Device) Close() {
	_ = ioctl(d.f.Fd(), uiDevDestroy, 0)
	d.f.Close()
	if d.Node != "" {
		_ = os.Remove(d.Node)
	}
}

// writeEvents 一次写一组事件（struct input_event：16 字节时间（内核会改成当前时间）+ type + code + value）。
func (d *Device) writeEvents(evs [][3]int32) error {
	buf := make([]byte, 0, 24*len(evs))
	for _, e := range evs {
		var b [24]byte
		binary.LittleEndian.PutUint16(b[16:], uint16(e[0]))
		binary.LittleEndian.PutUint16(b[18:], uint16(e[1]))
		binary.LittleEndian.PutUint32(b[20:], uint32(e[2]))
		buf = append(buf, b[:]...)
	}
	_, err := d.f.Write(buf)
	return err
}

func (d *Device) clamp(x, y float64) (int32, int32) {
	cx := math.Max(0, math.Min(float64(d.cfg.W-1), x))
	cy := math.Max(0, math.Min(float64(d.cfg.H-1), y))
	return int32(math.Round(cx)), int32(math.Round(cy))
}

// finger 一个手指当前的按压状态。
type finger struct {
	pressure, major, minor float64
}

func (d *Device) newFinger() finger {
	// 真手指：力度 0.2~0.45（0~255 刻度上 50~115）、接触面积 6~10mm 左右（这里按像素刻度 30~60）
	return finger{pressure: 50 + d.rnd.Float64()*65, major: 30 + d.rnd.Float64()*30, minor: 0}
}

func (f *finger) wobble(r *rand.Rand) {
	f.pressure = math.Max(20, math.Min(200, f.pressure+r.NormFloat64()*4))
	f.major = math.Max(15, math.Min(90, f.major+r.NormFloat64()*1.5))
	f.minor = f.major * (0.75 + r.Float64()*0.15)
}

func (d *Device) down(x, y float64, f finger) error {
	d.next++
	if d.next > 65535 {
		d.next = 1
	}
	px, py := d.clamp(x, y)
	return d.writeEvents([][3]int32{
		{evAbs, absMTSlot, 0},
		{evAbs, absMTTrackingID, d.next},
		{evAbs, absMTPositionX, px},
		{evAbs, absMTPositionY, py},
		{evAbs, absMTTouchMajor, int32(f.major)},
		{evAbs, absMTTouchMinor, int32(f.major * 0.8)},
		{evAbs, absMTPressure, int32(f.pressure)},
		{evKey, btnTouch, 1},
		{evKey, btnToolFinger, 1},
		{evSyn, synReport, 0},
	})
}

func (d *Device) move(x, y float64, f finger) error {
	px, py := d.clamp(x, y)
	return d.writeEvents([][3]int32{
		{evAbs, absMTSlot, 0},
		{evAbs, absMTPositionX, px},
		{evAbs, absMTPositionY, py},
		{evAbs, absMTTouchMajor, int32(f.major)},
		{evAbs, absMTTouchMinor, int32(f.minor)},
		{evAbs, absMTPressure, int32(f.pressure)},
		{evSyn, synReport, 0},
	})
}

func (d *Device) up() error {
	return d.writeEvents([][3]int32{
		{evAbs, absMTSlot, 0},
		{evAbs, absMTTrackingID, -1},
		{evKey, btnTouch, 0},
		{evKey, btnToolFinger, 0},
		{evSyn, synReport, 0},
	})
}

// Tap 点一下：落点一两个像素偏差，按住 50~110ms，中间手指轻微挪动一两次。
func (d *Device) Tap(x, y int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.rnd
	fx, fy := float64(x)+r.NormFloat64()*1.2, float64(y)+r.NormFloat64()*1.2
	f := d.newFinger()
	if err := d.down(fx, fy, f); err != nil {
		return err
	}
	hold := 50 + r.Intn(60)
	steps := 1 + r.Intn(2)
	for i := 0; i < steps; i++ {
		time.Sleep(time.Duration(hold/(steps+1)) * time.Millisecond)
		f.wobble(r)
		fx += r.NormFloat64() * 0.6
		fy += r.NormFloat64() * 0.6
		if err := d.move(fx, fy, f); err != nil {
			return err
		}
	}
	time.Sleep(time.Duration(hold/(steps+1)) * time.Millisecond)
	return d.up()
}

// Swipe 滑一下：120Hz 采样，先快后慢（ease-out），路线略带弧度、有一点点抖动，力度跟着变。
func (d *Device) Swipe(x1, y1, x2, y2, durMs int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.rnd
	if durMs < 60 {
		durMs = 60
	}
	if durMs > 5000 {
		durMs = 5000
	}
	dx, dy := float64(x2-x1), float64(y2-y1)
	length := math.Hypot(dx, dy)
	// 垂直方向的弧度：路线长度的 0~4%，方向随机
	bow := (r.Float64()*2 - 1) * 0.04 * length
	nx, ny := 0.0, 0.0
	if length > 0 {
		nx, ny = -dy/length, dx/length
	}
	f := d.newFinger()
	sx, sy := float64(x1)+r.NormFloat64(), float64(y1)+r.NormFloat64()
	if err := d.down(sx, sy, f); err != nil {
		return err
	}
	const frame = 8333 * time.Microsecond // 120Hz
	n := int(time.Duration(durMs)*time.Millisecond/frame) + 1
	start := time.Now()
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		e := 1 - math.Pow(1-t, 2.2) // 先快后慢
		arc := math.Sin(math.Pi*t) * bow
		x := sx + dx*e + nx*arc + r.NormFloat64()*0.5
		y := sy + dy*e + ny*arc + r.NormFloat64()*0.5
		f.wobble(r)
		if err := d.move(x, y, f); err != nil {
			return err
		}
		if wait := time.Until(start.Add(time.Duration(i) * frame)); wait > 0 {
			time.Sleep(wait)
		}
	}
	return d.up()
}
