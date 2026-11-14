package phones

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
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
	cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", name}, streamArgs(opt)...)...)
	cmd.Stdout = w
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动画面失败：%w", err)
	}
	if started != nil {
		started()
	}
	err := cmd.Wait()
	// 页面关了或被顶掉：把容器里的 screenrecord 也停掉
	_, _ = s.run.Run(context.Background(), "docker", "exec", name, "pkill", "-x", "screenrecord")
	if ctx.Err() != nil {
		return nil
	}
	return err
}
