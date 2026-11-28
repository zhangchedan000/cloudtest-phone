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
	gen    map[string]uint64
	next   uint64
}

// replace 顶掉这台手机正在播的那一路，登记新的一路，返回它的编号和登记后正在播的总路数
// （不同手机各算一路；同一台顶替不增加总数）。总路数用来按负载降质（见 degradeForLoad）。
func (t *streamTracker) replace(id string, cancel context.CancelFunc) (gen uint64, active int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel == nil {
		t.cancel = map[string]context.CancelFunc{}
		t.gen = map[string]uint64{}
	}
	if old := t.cancel[id]; old != nil {
		old()
	}
	t.next++
	t.cancel[id] = cancel
	t.gen[id] = t.next
	return t.next, len(t.cancel)
}

// finish 这一路结束：还是当前那一路就注销并返回 true；已经被新的一路顶掉了返回 false（别去动新的那一路）。
func (t *streamTracker) finish(id string, gen uint64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.gen[id] != gen {
		return false
	}
	delete(t.cancel, id)
	delete(t.gen, id)
	return true
}

// audNAL H.264 的「访问单元分隔符」（AUD，NAL 类型 9），6 个字节。
var audNAL = []byte{0, 0, 0, 1, 0x09, 0xf0}

// frameGap 画面停住这么久没新数据，就把最后一帧冲出去（补个 AUD）。只用于「停住后的最后一帧」，
// 活动中每一帧靠下面按真帧边界补的 AUD 分帧，和这个时间无关。
const frameGap = 60 * time.Millisecond

// frameSplitter 按 H.264 Annex-B 的真帧边界给流补 AUD（访问单元分隔符），而不是靠「停顿多久」猜。
//
// 为什么重写（2026-11-27 用户滚轮快滑时「撕裂」）：以前每次输出停顿 15ms 就补一个 AUD，
// 快速滚动时一帧数据大、管家读取被调度拖慢，一帧的数据在读取侧被隔开 >15ms，就被从**中间**切断、
// 浏览器解到半帧 → 撕裂。现在不看时间：扫描起始码，等上一帧的图像 NAL(1=P帧 / 5=IDR帧) 出现后、
// **下一个 NAL 之前**才补 AUD——这是真边界，不管读取被拖多久都不会把一帧切断。
// 画面停住（后面没有新 NAL 了）再靠 frameGap 补最后一个，把尾帧冲出去（那时帧已完整，安全）。
type frameSplitter struct {
	sawVCL bool   // 上一帧的图像 NAL 已出现，下一个 NAL 之前可以补 AUD 分帧
	carry  []byte // 末尾可能是没凑齐的起始码（跨 chunk），最多留 3 字节，下次拼上再判
}

// feed 处理一段数据，返回要写给浏览器的字节（原数据 + 在真帧边界插入的 AUD）。
func (s *frameSplitter) feed(data []byte) []byte {
	b := append(s.carry, data...)
	s.carry = nil
	out := make([]byte, 0, len(b)+12)
	emit := 0
	for i := 0; i+3 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 { // 起始码 00 00 01，NAL 头在 b[i+3]
			nalType := b[i+3] & 0x1f
			if s.sawVCL { // 到了新一帧的第一个 NAL：先把上一帧收尾（补 AUD）
				out = append(out, b[emit:i]...)
				out = append(out, audNAL...)
				emit = i
				s.sawVCL = false
			}
			if nalType == 1 || nalType == 5 { // 图像 NAL（P / IDR）
				s.sawVCL = true
			}
			i += 3 // 跳过这个起始码（for 的 i++ 再 +1）
		}
	}
	// 末尾最多留 3 字节（可能是没凑齐的起始码），其余放出
	if len(b)-emit > 3 {
		cut := len(b) - 3
		out = append(out, b[emit:cut]...)
		s.carry = append(s.carry, b[cut:]...)
	} else {
		s.carry = append(s.carry, b[emit:]...)
	}
	return out
}

// flush 画面停住 / 流结束：把没放出的尾巴 + （有半帧的话）一个 AUD 冲出去。
func (s *frameSplitter) flush() []byte {
	out := s.carry
	s.carry = nil
	if s.sawVCL {
		out = append(out, audNAL...)
		s.sawVCL = false
	}
	return out
}

// copyWithFrameEnds 把 screenrecord 的 H.264 裸流转给浏览器，按真帧边界补 AUD（见 frameSplitter）。
// 活动中的分帧与时间无关、不会撕裂；画面停住 gap 后把最后一帧冲出去（否则裸流里最后一帧要等下一个
// 起始码才显示，画面不动就一直黑着 / 停在上一帧，2026-11-15 真浏览器里测出）。
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
	var sp frameSplitter
	pending := false
	writeAll := func(p []byte) error {
		if len(p) == 0 {
			return nil
		}
		if _, err := w.Write(p); err != nil {
			go func() { // 浏览器断开了：把剩下的读完，让 screenrecord 能退出
				for range chunks {
				}
			}()
			return err
		}
		return nil
	}
	for {
		select {
		case b, ok := <-chunks:
			if !ok {
				if err := <-readErr; err != io.EOF {
					return err
				}
				return writeAll(sp.flush())
			}
			if err := writeAll(sp.feed(b)); err != nil {
				return err
			}
			pending = true
			timer.Reset(gap)
		case <-timer.C:
			if pending {
				if err := writeAll(sp.flush()); err != nil {
					return err
				}
				pending = false
			}
		}
	}
}

// hwStreamSoftCap 同时在看这么多路以内，画面质量不动。超过后开始降质。
// 为什么：RK3588 的硬件视频编码器（VPU）并发编码会话数有硬件上限（实测 6~7 路，踩坑 #176），
// 超了的那一路分配不到硬件、会退回软件编码（c2.android）吃 CPU。日常是「管很多台、只盯一两台」，
// 碰不到；真同时开很多路时，把码率/分辨率降下来——软编的那几路便宜些、硬编的也省带宽，比卡死强。
const hwStreamSoftCap = 4

// degradeForLoad 按当前同时在播的总路数给这一路降质：到软上限以内原样；超过后每多一路把码率往下压一档
// （×3/4），地板 600kbps；再高（软上限 +2 起）把分辨率长边也封到 540，软编更省 CPU。纯函数，好单测。
func degradeForLoad(opt StreamOptions, active int) StreamOptions {
	if active <= hwStreamSoftCap {
		return opt
	}
	br := opt.BitRate
	if br < 200_000 || br > 20_000_000 {
		br = 2_000_000 // 和 streamArgs 的默认值一致
	}
	for i := hwStreamSoftCap; i < active && br > 600_000; i++ {
		br = br * 3 / 4
	}
	if br < 600_000 {
		br = 600_000
	}
	opt.BitRate = br
	if active >= hwStreamSoftCap+2 {
		opt.Width, opt.Height = capLongSide(opt.Width, opt.Height, 540)
	}
	return opt
}

// capLongSide 把宽高按比例缩到长边不超过 max（已经更小就不动；缺尺寸就不动）。16 对齐留给 streamArgs。
func capLongSide(w, h, max int) (int, int) {
	long := w
	if h > long {
		long = h
	}
	if long <= 0 || long <= max {
		return w, h
	}
	return w * max / long, h * max / long
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
	gen, active := s.streams.replace(id, cancel)
	name := containerName(id)
	// 结束时只有自己还是当前那一路才去停 screenrecord：被顶掉的旧一路在新一路起来后才走到这里，
	// 以前无条件 pkill 会把新一路的 screenrecord 也杀掉（两个窗口看同一台时互相踢、画面反复重连）。
	defer func() {
		if s.streams.finish(id, gen) {
			_, _ = s.run.Run(context.Background(), "docker", "exec", name, "pkill", "-x", "screenrecord")
		}
	}()
	// 顶掉旧的一路：docker exec 的进程被杀不会连带杀掉容器里的 screenrecord，要进容器停。
	_, _ = s.run.Run(ctx, "docker", "exec", name, "pkill", "-x", "screenrecord")
	// 横屏时画面是横的：宽高按当前方向对调（页面按竖屏要的尺寸），方向一变就结束这一路，页面自动重连拿新方向（rotate.go）
	if st, err := s.screen(ctx, id, true); err == nil {
		if st.landscape() != (opt.Width > opt.Height) {
			opt.Width, opt.Height = opt.Height, opt.Width
		}
		go s.watchRotation(ctx, id, st.landscape(), cancel)
	}
	opt = degradeForLoad(opt, active) // 同时在看的路数多时降质，别把 VPU / CPU 压垮（踩坑 #176）
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
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// watchRotation 实时画面播着的时候每 2 秒看一次横竖屏，变了就结束这一路（页面会马上重连，按新方向出画面）。
func (s *Service) watchRotation(ctx context.Context, id string, landscape bool, stop func()) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if st, err := s.screen(ctx, id, true); err == nil && st.landscape() != landscape {
				stop()
				return
			}
		}
	}
}
