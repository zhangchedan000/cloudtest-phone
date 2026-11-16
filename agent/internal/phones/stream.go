package phones

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// 实时画面：容器里跑安卓自带的 screenrecord，输出 H.264 裸流（Annex-B），管家程序原样转给浏览器，
// 浏览器用 WebCodecs 解码。比定时截屏流畅得多，带宽也小。
// screenrecord 单次最长 180 秒，到时间浏览器自动重连。
// 同一台设备同一时间只放一路（新的连上来就停掉旧的），关掉页面就停。

// StreamOptions 画面参数。
type StreamOptions struct {
	Width   int // 0 = 原始分辨率；一般传 540 左右，省带宽
	Height  int
	BitRate int // bit/s，0 = 2 Mbps
}

type streamTracker struct {
	mu     sync.Mutex
	cancel map[string]context.CancelFunc
}

func (t *streamTracker) replace(id string, cancel context.CancelFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel == nil {
		t.cancel = map[string]context.CancelFunc{}
	}
	if old := t.cancel[id]; old != nil {
		old()
	}
	t.cancel[id] = cancel
}

// audNAL H.264 的「访问单元分隔符」（AUD，NAL 类型 9），6 个字节。
var audNAL = []byte{0, 0, 0, 1, 0x09, 0xf0}

// frameGap screenrecord 一帧是一口气写完的；停了这么久没新数据，就认为这一帧写完了。
const frameGap = 15 * time.Millisecond

// copyWithFrameEnds 把 screenrecord 的输出原样转给浏览器，每次停顿（一帧写完）后补一个 AUD。
//
// 为什么：裸流里一个 NAL 在哪结束，要等下一个起始码到了才知道，所以浏览器总是晚一帧显示；
// 画面不动时 screenrecord 不再出新帧，最后一帧（包括刚打开时的第一帧）就一直显示不出来——
// 打开详情页是黑屏、点完按钮画面停在上一帧（2026-11-15 真浏览器里测出）。
// 补上 AUD 后，上一帧马上有了结尾；浏览器认出结尾的 AUD 就立刻解码显示。
// 旧版页面不认这个 AUD 也没关系：它会被当成下一帧开头的分隔符，标准允许。
func copyWithFrameEnds(w io.Writer, r io.Reader, gap time.Duration) error {
	chunks := make(chan []byte, 16)
	readErr := make(chan error, 1)
	go func() {
		defer close(chunks)
		for {
			buf := make([]byte, 64<<10)
			n, err := r.Read(buf)
			if n > 0 {
				chunks <- buf[:n]
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	pending := false
	for {
		select {
		case b, ok := <-chunks:
			if !ok {
				if err := <-readErr; err != io.EOF {
					return err
				}
				return nil
			}
			if _, err := w.Write(b); err != nil {
				go func() { // 浏览器断开了：把剩下的读完，让 screenrecord 能退出
					for range chunks {
					}
				}()
				return err
			}
			pending = true
			timer.Reset(gap)
		case <-timer.C:
			if pending {
				if _, err := w.Write(audNAL); err != nil {
					return err
				}
				pending = false
			}
		}
	}
}

// streamArgs screenrecord 的参数（参数不对就用默认值，不报错）。
func streamArgs(opt StreamOptions) []string {
	args := []string{"screenrecord", "--output-format=h264", "--time-limit", "180"}
	if opt.Width >= 160 && opt.Width <= 4096 && opt.Height >= 160 && opt.Height <= 4096 {
		// 硬件编码器要求宽高是 16 的倍数
		args = append(args, "--size", fmt.Sprintf("%dx%d", opt.Width/16*16, opt.Height/16*16))
	}
	br := opt.BitRate
	if br < 200_000 || br > 20_000_000 {
		br = 2_000_000
	}
	return append(args, "--bit-rate", strconv.Itoa(br), "-")
}

// Stream 把设备画面（H.264 裸流）写到 w，直到 ctx 结束、screenrecord 到时间或被新的一路顶掉。
func (s *Service) Stream(ctx context.Context, id string, opt StreamOptions, w io.Writer, started func()) error {
	if err := s.requireRunning(ctx, id, true); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.streams.replace(id, cancel)
	name := containerName(id)
	// 顶掉旧的一路：docker exec 的进程被杀不会连带杀掉容器里的 screenrecord，要进容器停。
	_, _ = s.run.Run(ctx, "docker", "exec", name, "pkill", "-x", "screenrecord")
	cmd := s.phoneCommand(ctx, id, streamArgs(opt)...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("启动画面失败：%w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动画面失败：%w", err)
	}
	if started != nil {
		started()
	}
	_ = copyWithFrameEnds(w, out, frameGap)
	err = cmd.Wait()
	// 页面关了或被顶掉：把容器里的 screenrecord 也停掉
	_, _ = s.run.Run(context.Background(), "docker", "exec", name, "pkill", "-x", "screenrecord")
	if ctx.Err() != nil {
		return nil
	}
	return err
}
