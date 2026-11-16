package phones

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// 剪贴板互通（2026-11-15 用户要求）：电脑 ↔ 手机互相复制粘贴，支持中文。
//
// 安卓 14 没有读写剪贴板的 shell 命令。做法（和 scrcpy 一样）：在容器里用 app_process 跑一个 4 KB 的小程序
// （tools/clipper/Clip.java，编译结果 pp-clip.dex 内嵌在这里），以 shell 身份直接调系统剪贴板服务。
// 不装 App、不装 ADB 键盘（项目规矩）。
//
// 顺带解决中文输入：安卓 input text 只能打英文；中文先放进剪贴板，再模拟按「粘贴」键（KEYCODE_PASTE）。
// 踩坑 #30：docker exec 进去没有 BOOTCLASSPATH 等环境变量，app_process 会什么都不做直接退出，
// 要先从 /init.environ.rc 和 /data/system/environ/classpath 读进来（clipWrapper 做这件事）。

//go:embed pp-clip.dex
var clipDex []byte

// clipWrapper 在容器里补上 app_process 需要的环境变量再运行小程序。
const clipWrapper = `for f in /init.environ.rc /data/system/environ/classpath; do
  while read -r k n v; do [ "$k" = export ] && export "$n=$v"; done < "$f"
done
export CLASSPATH="$PP_CLIP_DEX"
exec app_process / Clip "$@"
`

// MaxClipboard 一次最多传多少字节的文字。
const MaxClipboard = 100 << 10

// clipPaths 容器里的小程序和启动脚本路径；文件名带内容的哈希，升级后自动换新的。
func clipPaths() (dex, sh string) {
	sum := sha256.Sum256(append(append([]byte{}, clipDex...), clipWrapper...))
	tag := hex.EncodeToString(sum[:4])
	return "/data/local/tmp/pp-clip-" + tag + ".dex", "/data/local/tmp/pp-clip-" + tag + ".sh"
}

// ensureClipTool 小程序不在手机里就放进去（重置 / 刷机会清空 /data/local/tmp）。
func (s *Service) ensureClipTool(ctx context.Context, name string) (string, error) {
	dex, sh := clipPaths()
	if _, err := s.run.Run(ctx, "docker", "exec", name, "test", "-f", sh); err == nil {
		return sh, nil
	}
	dir := filepath.Join(s.cfg.DataDir, "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for remote, content := range map[string][]byte{dex: clipDex, sh: []byte(clipWrapper)} {
		f, err := os.CreateTemp(dir, "clip-*")
		if err != nil {
			return "", err
		}
		_, werr := f.Write(content)
		_ = f.Close()
		if werr == nil {
			var out string
			out, werr = s.run.Run(ctx, "docker", "cp", f.Name(), name+":"+remote)
			if werr != nil {
				werr = fmt.Errorf("%v %s", werr, out)
			}
		}
		_ = os.Remove(f.Name())
		if werr != nil {
			return "", fmt.Errorf("剪贴板工具传进手机失败：%v", werr)
		}
	}
	// dex 要让 shell 用户（小程序会切成 shell）能读
	_, _ = s.run.Run(ctx, "docker", "exec", name, "chmod", "644", dex, sh)
	return sh, nil
}

func (s *Service) runClip(ctx context.Context, id string, args ...string) (string, error) {
	if err := s.requireRunning(ctx, id, true); err != nil {
		return "", err
	}
	name := containerName(id)
	sh, err := s.ensureClipTool(ctx, name)
	if err != nil {
		return "", err
	}
	dex, _ := clipPaths()
	cmd := append([]string{"exec", "-e", "PP_CLIP_DEX=" + dex, name, "sh", sh}, args...)
	out, err := s.run.Run(ctx, "docker", cmd...)
	// app_process 之前可能打出别的行，只看最后一行
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if err != nil || strings.HasPrefix(last, "error") || last == "" {
		return "", fmt.Errorf("剪贴板操作失败：%s", strings.TrimSpace(strings.TrimPrefix(last, "error")+" "+errText(err)))
	}
	return last, nil
}

func checkClipText(text string) error {
	if text == "" {
		return fmt.Errorf("%w：文字是空的", ErrInvalid)
	}
	if len(text) > MaxClipboard {
		return fmt.Errorf("%w：文字太长（最多 100 KB）", ErrInvalid)
	}
	if !utf8.ValidString(text) {
		return fmt.Errorf("%w：文字编码不对", ErrInvalid)
	}
	return nil
}

// SetClipboard 把文字放进手机剪贴板；paste=true 时再按一下「粘贴」，直接打进当前输入框。
func (s *Service) SetClipboard(ctx context.Context, id, text string, paste bool) error {
	if err := checkClipText(text); err != nil {
		return err
	}
	if _, err := s.runClip(ctx, id, "set", base64.StdEncoding.EncodeToString([]byte(text))); err != nil {
		return err
	}
	if paste {
		if out, err := s.run.Run(ctx, "docker", "exec", containerName(id), "input", "keyevent", "KEYCODE_PASTE"); err != nil {
			return fmt.Errorf("粘贴失败：%v %s", err, out)
		}
	}
	return nil
}

// GetClipboard 读手机剪贴板里的文字；空的返回 ""。
func (s *Service) GetClipboard(ctx context.Context, id string) (string, error) {
	last, err := s.runClip(ctx, id, "get")
	if err != nil {
		return "", err
	}
	if last == "empty" {
		return "", nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(last, "ok")))
	if err != nil || !strings.HasPrefix(last, "ok ") {
		return "", fmt.Errorf("剪贴板内容读不出来：%q", last)
	}
	return string(b), nil
}

// pasteKeepClipboard 打中文（电脑键盘打字）：放进剪贴板、粘贴，再把手机原来的剪贴板内容放回去，
// 手机剪贴板里不留我们打的字（踩坑 #62）。原来是空的就没法「放回空」，只能留着这次的字。
func (s *Service) pasteKeepClipboard(ctx context.Context, id, text string) error {
	old, oldErr := s.GetClipboard(ctx, id)
	if err := s.SetClipboard(ctx, id, text, true); err != nil {
		return err
	}
	if oldErr == nil && old != "" && old != text {
		time.Sleep(300 * time.Millisecond) // 等 App 把粘贴读完
		_, _ = s.runClip(ctx, id, "set", base64.StdEncoding.EncodeToString([]byte(old)))
	}
	return nil
}
