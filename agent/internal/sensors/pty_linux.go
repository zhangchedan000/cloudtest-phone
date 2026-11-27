package sensors

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// tcflsh ioctl TCFLSH（arm64 / amd64 都是 0x540B，syscall 包没导出）。
const tcflsh = 0x540B

// tiocgptpeer ioctl TIOCGPTPEER（_IO('T', 0x41)，Linux 4.13+）：凭主端直接拿到它的从端，不经过 /dev/pts 路径。
const tiocgptpeer = 0x5441

// PipePaths 模拟器传感器 HAL 打开的管道文件（踩坑 #59）：安卓 14 起是 goldfish_pipe_dprctd（镜像里的 HAL 打了补丁才走它）；
// 安卓 13 的 HAL 连 vsock 失败会自己退回 /dev/goldfish_pipe。两个都绑到同一个伪终端，哪个版本都能用。
var PipePaths = []string{"/dev/goldfish_pipe_dprctd", "/dev/goldfish_pipe"}

// RealPipePaths 新镜像（标签 prism.names=1）里传感器驱动打开的路径：host/image/rename-emu.py 把上面两个换成了
// 联发科手机真实的设备名（App 列 /dev 看不到 goldfish 字样，踩坑 #75）。
var RealPipePaths = []string{"/dev/hwmsensor", "/dev/sensorlist"}

func ioctl(fd uintptr, req uintptr, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

// makeRaw 关掉终端的回显、换行转换等，让字节原样通过（cfmakeraw）。
func makeRaw(fd uintptr) error {
	var t syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, uintptr(unsafe.Pointer(&t))); err != nil {
		return err
	}
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB
	t.Cflag |= syscall.CS8
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0
	return ioctl(fd, syscall.TCSETS, uintptr(unsafe.Pointer(&t)))
}

// OpenPTY 在当前（容器的）/dev/ptmx 上开一对伪终端，都设成原始模式。
func OpenPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if err = ioctl(master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		master.Close()
		return nil, nil, err
	}
	var n uint32
	if err = ioctl(master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		master.Close()
		return nil, nil, err
	}
	name := fmt.Sprintf("/dev/pts/%d", n)
	// 先用 TIOCGPTPEER 凭主端拿从端（踩坑 #124）：安卓 init 开机时会在 /dev/pts 上再挂一个新的 devpts，
	// 挂上之后「/dev/pts/N」指到新实例里——要么没有、要么是安卓自己的别的终端。小助手在负载高时慢一拍，
	// 正好撞上就配不上串口（蓝牙 HAL 打不开 /dev/ttyS1 → 连崩 6 次放弃 → 蓝牙关着）。老内核没有这个 ioctl 再按路径开。
	if fd, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocgptpeer, uintptr(os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC)); e == 0 {
		slave = os.NewFile(fd, name)
	} else if slave, err = os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0); err != nil {
		master.Close()
		return nil, nil, err
	}
	_ = slave.Chmod(0o666)
	_ = makeRaw(master.Fd())
	_ = makeRaw(slave.Fd())
	return master, slave, nil
}

// Setup 开伪终端并把从端绑到 PipePaths（在容器的挂载命名空间里、安卓 init 启动之前调用）。
func Setup() (master, slave *os.File, err error) { return SetupPaths(PipePaths) }

// SetupPaths 同上，绑到给定的路径。
func SetupPaths(paths []string) (master, slave *os.File, err error) {
	master, slave, err = OpenPTY()
	if err != nil {
		return nil, nil, fmt.Errorf("开伪终端：%w", err)
	}
	for _, path := range paths {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o666); err == nil {
			f.Close()
		}
		if err := bindPTY(slave, path); err != nil {
			master.Close()
			slave.Close()
			return nil, nil, fmt.Errorf("绑到 %s：%w", path, err)
		}
		_ = os.Chmod(path, 0o666)
	}
	return master, slave, nil
}

// Serve 一直服务（不返回）。slave 自己也一直开着：HAL 重启、暂时没人打开从端时，主端读写不会出错。
func Serve(spec Spec, master, slave *os.File, logf func(string)) {
	out := make(chan string, 64)
	f := NewFeeder(spec, chanWriter(out))
	activeFeeder.Store(f) // 摇一摇：登记当前 Feeder
	f.Log = logf
	flushToHAL := func() {
		// HAL 重启时，上一轮没被读走的数据还在从端的输入队列里，新 HAL 会把它当成 list-sensors 的回答而崩溃：清掉
		for len(out) > 0 {
			<-out
		}
		time.Sleep(20 * time.Millisecond)
		_ = ioctl(slave.Fd(), tcflsh, 0) // 0 = TCIFLUSH：清掉从端还没读的输入
	}
	go func() {
		for s := range out {
			_, _ = master.Write([]byte(s))
		}
	}()
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 4096)
		for {
			n, err := master.Read(tmp)
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			buf = Frames(append(buf, tmp[:n]...), func(msg string) {
				switch {
				case strings.HasPrefix(msg, "pipe:"):
					f.Reset()
					flushToHAL()
				case msg == "list-sensors":
					flushToHAL()
				}
				f.Handle(msg)
			})
		}
	}()
	last := time.Now()
	for {
		time.Sleep(f.Delay())
		now := time.Now()
		_ = f.Tick(now, now.Sub(last).Seconds())
		last = now
	}
}

// chanWriter 每次 Write 是一条完整消息：放进队列，满了就丢（传感器数据丢几条无所谓，不能卡住）。
type chanWriter chan string

func (c chanWriter) Write(p []byte) (int, error) {
	select {
	case c <- string(p):
	default:
	}
	return len(p), nil
}

// ModemPath 电话驱动（rild + reference-ril）连的基带设备（镜像里 init.radio.rc 配的，踩坑 #65）。
const ModemPath = "/dev/ttyC0"

// SetupModem 开一个伪终端绑到 ModemPath，属主 root:radio 0660（rild 以 radio 身份打开）。
func SetupModem() (master, slave *os.File, err error) {
	return SetupTTY(ModemPath, 1001) // radio 组
}

// FlushInput 清掉伪终端从端还没被读走的输入（TCIFLUSH）。
func FlushInput(slave *os.File) { _ = ioctl(slave.Fd(), tcflsh, 0) }

// BTPath 蓝牙 HAL 打开的串口（镜像里 init.bluetooth.rc 设的 vendor.ser.bt-uart，踩坑 #48）。
const BTPath = "/dev/ttyS1"

// SetupBT 开一个伪终端绑到 BTPath，属主 root:bluetooth 0660（蓝牙 HAL 以 bluetooth 身份打开）。
func SetupBT() (master, slave *os.File, err error) {
	return SetupTTY(BTPath, 1002) // bluetooth 组
}

// SetupTTY 开一个伪终端，从端绑到 path（属主 root:gid 0660）。
func SetupTTY(path string, gid int) (master, slave *os.File, err error) {
	master, slave, err = OpenPTY()
	if err != nil {
		return nil, nil, fmt.Errorf("开伪终端：%w", err)
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o660); err == nil {
		f.Close()
	}
	if err := bindPTY(slave, path); err != nil {
		master.Close()
		slave.Close()
		return nil, nil, fmt.Errorf("绑到 %s：%w", path, err)
	}
	_ = os.Chown(path, 0, gid)
	_ = os.Chmod(path, 0o660)
	return master, slave, nil
}

// bindPTY 把伪终端从端绑到 path。用 /proc/self/fd/N 当来源（指的就是手里这个文件），不用「/dev/pts/N」路径：
// 安卓 init 在 /dev/pts 上挂了新 devpts 之后，那个路径就不是我们的终端了（踩坑 #124）。
// 短暂失败重试几次；/proc 用不了才退回按路径绑。
func bindPTY(slave *os.File, path string) error {
	src := fmt.Sprintf("/proc/self/fd/%d", slave.Fd())
	var err error
	for i := 0; i < 5; i++ {
		if err = syscall.Mount(src, path, "", syscall.MS_BIND, ""); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err2 := syscall.Mount(slave.Name(), path, "", syscall.MS_BIND, ""); err2 == nil {
		return nil
	}
	return err
}
