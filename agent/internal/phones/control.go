package phones

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// 远程画面和操作：截一张屏、点一下、划一下、输入文字、按键。
// 都在容器里用安卓自带的 screencap / input 命令完成，宿主机不需要 ADB。

// RawRunner 执行命令并原样返回标准输出（截图是二进制 PNG，不能和报错混在一起）。
type RawRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRaw struct{}

func (execRaw) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

// ErrNotRunning 设备没开机时不能截屏或操作；ErrNotReady 容器起来了但安卓还没开机完成。
var (
	ErrNotRunning = errors.New("设备没有开机")
	ErrNotReady   = errors.New("还在开机，等开机完成再操作")
)

// bootTracker 记住哪次开机已经完成（按容器的启动时间区分），不用每次都进容器问。
type bootTracker struct {
	mu     sync.Mutex
	booted map[string]string // 设备 ID → 已开机完成的那次容器启动时间
}

func (b *bootTracker) done(id, startedAt string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return startedAt != "" && b.booted[id] == startedAt
}

func (b *bootTracker) forget(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.booted, id)
}

func (b *bootTracker) mark(id, startedAt string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.booted == nil {
		b.booted = map[string]string{}
	}
	b.booted[id] = startedAt
}

// readyProbe 在容器里问：开机完成没有、input 服务在不在。
const readyProbe = "getprop sys.boot_completed; service check input"

func readyOutput(out string) bool {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return len(lines) >= 2 && strings.TrimSpace(lines[0]) == "1" && strings.HasSuffix(strings.TrimSpace(lines[len(lines)-1]), ": found")
}

// State 设备的真实状态：stopped（容器没在跑）/ starting（容器在跑，安卓还没开机完成，
// 或者开机后设置还没做完——设置语言会重启一次安卓框架）/ running。
func (s *Service) State(ctx context.Context, id string) string {
	st := s.androidState(ctx, id)
	if st == "running" {
		if set := s.settings.get(id); set != nil && set.State == "pending" {
			return "starting"
		}
	}
	return st
}

// androidState 只看安卓本身有没有就绪（不管开机后设置）。
func (s *Service) androidState(ctx context.Context, id string) string {
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	if err != nil || !info.State.Running {
		return "stopped"
	}
	if s.boots.done(id, info.State.StartedAt) {
		return "running"
	}
	// 实测（踩坑 #15）：sys.boot_completed=1 之后安卓框架还可能重启（例如改了语言），
	// 这段时间 input 服务不在，操作会失败。所以要「开机完成」并且「input 服务在」才算运行中。
	out, _ := s.run.Run(ctx, "docker", "exec", containerName(id), "sh", "-c", readyProbe)
	if !readyOutput(out) {
		return "starting"
	}
	s.boots.mark(id, info.State.StartedAt)
	return "running"
}

// requireRunning 截屏只要容器在跑（开机画面也能看）；操作要等安卓开机完成（ready）。
func (s *Service) requireRunning(ctx context.Context, id string, ready bool) error {
	st := s.store.Snapshot()
	if _, err := st.Phone(id); err != nil {
		return err
	}
	switch s.State(ctx, id) {
	case "stopped":
		return ErrNotRunning
	case "starting":
		if ready {
			return ErrNotReady
		}
	}
	return nil
}

// Screenshot 返回设备当前画面（PNG）。
func (s *Service) Screenshot(ctx context.Context, id string) ([]byte, error) {
	if err := s.requireRunning(ctx, id, false); err != nil {
		return nil, err
	}
	png, err := s.raw.Output(ctx, "docker", "exec", containerName(id), "screencap", "-p")
	if err != nil {
		return nil, fmt.Errorf("截屏失败：%w", err)
	}
	if len(png) < 8 || string(png[1:4]) != "PNG" {
		return nil, errors.New("截屏失败：没有拿到图片（可能还在开机）")
	}
	return png, nil
}

// InputRequest 一次操作。坐标是安卓屏幕上的像素。
type InputRequest struct {
	Kind     string `json:"kind"` // tap / swipe / text / key
	X        int    `json:"x"`
	Y        int    `json:"y"`
	X2       int    `json:"x2"`
	Y2       int    `json:"y2"`
	Duration int    `json:"durationMs"`
	Text     string `json:"text"`
	Key      string `json:"key"` // back / home / recents / power / enter / delete / volume_up / volume_down / tab / up … / select_all / copy …
}

var keyCodes = map[string]string{
	"back": "KEYCODE_BACK", "home": "KEYCODE_HOME", "recents": "KEYCODE_APP_SWITCH", "power": "KEYCODE_POWER",
	"enter": "KEYCODE_ENTER", "delete": "KEYCODE_DEL", "volume_up": "KEYCODE_VOLUME_UP", "volume_down": "KEYCODE_VOLUME_DOWN",
	// 电脑键盘直接打字用（2026-11-16 用户要求）
	"forward_delete": "KEYCODE_FORWARD_DEL", "tab": "KEYCODE_TAB", "escape": "KEYCODE_ESCAPE",
	"up": "KEYCODE_DPAD_UP", "down": "KEYCODE_DPAD_DOWN", "left": "KEYCODE_DPAD_LEFT", "right": "KEYCODE_DPAD_RIGHT",
	"move_home": "KEYCODE_MOVE_HOME", "move_end": "KEYCODE_MOVE_END", "page_up": "KEYCODE_PAGE_UP", "page_down": "KEYCODE_PAGE_DOWN",
}

// keyCombos Ctrl+字母组合键（input keycombination，安卓 13 起有）。
var keyCombos = map[string]string{
	"select_all": "KEYCODE_A", "copy": "KEYCODE_C", "cut": "KEYCODE_X", "undo": "KEYCODE_Z", "redo": "KEYCODE_Y",
}

// inputArgs 把一次操作变成 `input …` 的参数；参数不对返回 ErrInvalid。
func inputArgs(req InputRequest) ([]string, error) {
	coord := func(v int) error {
		if v < 0 || v > 10000 {
			return fmt.Errorf("%w：坐标超出范围", ErrInvalid)
		}
		return nil
	}
	switch req.Kind {
	case "tap":
		if err := errors.Join(coord(req.X), coord(req.Y)); err != nil {
			return nil, err
		}
		return []string{"tap", strconv.Itoa(req.X), strconv.Itoa(req.Y)}, nil
	case "swipe":
		if err := errors.Join(coord(req.X), coord(req.Y), coord(req.X2), coord(req.Y2)); err != nil {
			return nil, err
		}
		d := req.Duration
		if d <= 0 {
			d = 300
		}
		if d > 5000 {
			d = 5000
		}
		return []string{"swipe", strconv.Itoa(req.X), strconv.Itoa(req.Y), strconv.Itoa(req.X2), strconv.Itoa(req.Y2), strconv.Itoa(d)}, nil
	case "text":
		if req.Text == "" || len(req.Text) > 500 {
			return nil, fmt.Errorf("%w：文字为空或太长", ErrInvalid)
		}
		if !isPlainASCII(req.Text) {
			// Input 会先把这种文字改走剪贴板；走到这里说明调用方式不对
			return nil, fmt.Errorf("%w：安卓 input 命令只能输入英文、数字和常用符号", ErrInvalid)
		}
		// input text 把空格写成 %s；其余特殊字符交给 shell 前要整体加引号。
		return []string{"text", shellQuote(strings.ReplaceAll(req.Text, " ", "%s"))}, nil
	case "key":
		if letter, ok := keyCombos[req.Key]; ok {
			return []string{"keycombination", "KEYCODE_CTRL_LEFT", letter}, nil
		}
		code, ok := keyCodes[req.Key]
		if !ok {
			return nil, fmt.Errorf("%w：不认识的按键 %q", ErrInvalid, req.Key)
		}
		return []string{"keyevent", code}, nil
	}
	return nil, fmt.Errorf("%w：不认识的操作 %q", ErrInvalid, req.Kind)
}

// shellQuote 安卓的 input 由 sh 解释，单引号包起来最稳。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isPlainASCII 安卓 input text 能直接打的文字（英文、数字、常用符号）。
func isPlainASCII(text string) bool {
	for _, r := range text {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// Input 在设备上执行一次操作。
// 中文等 input text 打不了的文字：放进手机剪贴板再按「粘贴」（见 clipboard.go）。
func (s *Service) Input(ctx context.Context, id string, req InputRequest) error {
	if req.Kind == "text" && req.Text != "" && !isPlainASCII(req.Text) {
		if len(req.Text) > 5000 {
			return fmt.Errorf("%w：文字太长（发送文字最多 5000 字节，长文请用「放进手机剪贴板」）", ErrInvalid)
		}
		return s.SetClipboard(ctx, id, req.Text, true)
	}
	args, err := inputArgs(req)
	if err != nil {
		return err
	}
	if err := s.requireRunning(ctx, id, true); err != nil {
		return err
	}
	cmd := "input " + strings.Join(args, " ")
	if out, err := s.run.Run(ctx, "docker", "exec", containerName(id), "sh", "-c", cmd); err != nil {
		return fmt.Errorf("操作失败：%v %s", err, out)
	}
	return nil
}

// Logs 最近的安卓日志（logcat），排查开不了机、App 闪退用。level：V/D/I/W/E，默认 W 只看警告和错误。
func (s *Service) Logs(ctx context.Context, id string, lines int, level string) (string, error) {
	if err := s.requireRunning(ctx, id, false); err != nil {
		return "", err
	}
	if lines <= 0 || lines > 2000 {
		lines = 300
	}
	switch level {
	case "V", "D", "I", "W", "E":
	default:
		level = "W"
	}
	out, err := s.run.Run(ctx, "docker", "exec", containerName(id), "logcat", "-d", "-t", strconv.Itoa(lines), "*:"+level)
	if err != nil {
		return "", fmt.Errorf("读日志失败：%v %s", err, out)
	}
	return out, nil
}
