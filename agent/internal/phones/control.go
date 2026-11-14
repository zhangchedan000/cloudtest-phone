package phones

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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

// ErrNotRunning 设备没开机时不能截屏或操作。
var ErrNotRunning = errors.New("设备没有开机")

func (s *Service) requireRunning(ctx context.Context, id string) error {
	st := s.store.Snapshot()
	if _, err := st.Phone(id); err != nil {
		return err
	}
	if !s.isRunning(ctx, id) {
		return ErrNotRunning
	}
	return nil
}

// Screenshot 返回设备当前画面（PNG）。
func (s *Service) Screenshot(ctx context.Context, id string) ([]byte, error) {
	if err := s.requireRunning(ctx, id); err != nil {
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
	Key      string `json:"key"` // back / home / recents / power / enter / delete / volume_up / volume_down
}

var keyCodes = map[string]string{
	"back": "KEYCODE_BACK", "home": "KEYCODE_HOME", "recents": "KEYCODE_APP_SWITCH", "power": "KEYCODE_POWER",
	"enter": "KEYCODE_ENTER", "delete": "KEYCODE_DEL", "volume_up": "KEYCODE_VOLUME_UP", "volume_down": "KEYCODE_VOLUME_DOWN",
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
		for _, r := range req.Text {
			if r > unicode.MaxASCII || !unicode.IsPrint(r) {
				return nil, fmt.Errorf("%w：安卓 input 命令只能输入英文、数字和常用符号", ErrInvalid)
			}
		}
		// input text 把空格写成 %s；其余特殊字符交给 shell 前要整体加引号。
		return []string{"text", shellQuote(strings.ReplaceAll(req.Text, " ", "%s"))}, nil
	case "key":
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

// Input 在设备上执行一次操作。
func (s *Service) Input(ctx context.Context, id string, req InputRequest) error {
	args, err := inputArgs(req)
	if err != nil {
		return err
	}
	if err := s.requireRunning(ctx, id); err != nil {
		return err
	}
	cmd := "input " + strings.Join(args, " ")
	if out, err := s.run.Run(ctx, "docker", "exec", containerName(id), "sh", "-c", cmd); err != nil {
		return fmt.Errorf("操作失败：%v %s", err, out)
	}
	return nil
}
