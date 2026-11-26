package phones

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
)

// 应用列表（2026-11-26 用户要求，对齐魔云腾）：列出手机里有桌面图标的 App，一键打开 / 卸载。
// 名字 / 图标是 APK 资源，shell 取不到；v1 先给包名 + 是不是用户自己装的，前端做常见 App 的友好名映射。
// 命令（cmd package / pm / monkey）要在手机自己的 SELinux 身份里跑（踩坑 #135），走 phoneCommand（命令小助手）。

var pkgRe = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.]{1,254}$`)

// AppInfo 一个应用。
type AppInfo struct {
	Package string `json:"package"`
	User    bool   `json:"user"` // 用户自己装的（非系统预装）
}

// phoneOut 在手机里跑命令并取输出。s.run 已被 phoneExecRunner 包过：开了真 SELinux 的手机会把
// docker exec 自动改走命令小助手（带手机身份），和剪贴板 / input 一个路子（踩坑 #135）。
func (s *Service) phoneOut(ctx context.Context, id string, args ...string) (string, error) {
	return s.run.Run(ctx, "docker", append([]string{"exec", containerName(id)}, args...)...)
}

// Apps 列出手机里有桌面图标的应用（可启动的），用户自己装的排前面。
func (s *Service) Apps(ctx context.Context, id string) ([]AppInfo, error) {
	if err := s.requireRunning(ctx, id, true); err != nil {
		return nil, err
	}
	launch, err := s.phoneOut(ctx, id, "cmd", "package", "query-activities", "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LAUNCHER")
	log.Printf("DEBUG apps %s: sock=%q err=%v len=%d hasPkg=%v head=%q", id, s.cmdSock(containerName(id)), err, len(launch), strings.Contains(launch, "packageName="), strings.TrimSpace(launch[:min(120, len(launch))]))
	if err != nil {
		return nil, fmt.Errorf("列应用失败：%v", err)
	}
	// 用户自己装的包（标记一下，排前面、卸载更安全）
	userSet := map[string]bool{}
	if u, err := s.phoneOut(ctx, id, "pm", "list", "packages", "-3"); err == nil {
		for _, l := range strings.Split(u, "\n") {
			if p := strings.TrimPrefix(strings.TrimSpace(l), "package:"); p != "" && strings.HasPrefix(strings.TrimSpace(l), "package:") {
				userSet[p] = true
			}
		}
	}
	seen := map[string]bool{}
	var out []AppInfo
	for _, l := range strings.Split(launch, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "packageName=") {
			continue
		}
		pkg := strings.TrimPrefix(l, "packageName=")
		if !pkgRe.MatchString(pkg) || seen[pkg] {
			continue
		}
		seen[pkg] = true
		out = append(out, AppInfo{Package: pkg, User: userSet[pkg]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].User != out[j].User {
			return out[i].User // 用户装的排前
		}
		return out[i].Package < out[j].Package
	})
	return out, nil
}

// LaunchApp 打开一个应用。
func (s *Service) LaunchApp(ctx context.Context, id, pkg string) error {
	if !pkgRe.MatchString(pkg) {
		return fmt.Errorf("%w：包名不对", ErrInvalid)
	}
	if err := s.requireRunning(ctx, id, true); err != nil {
		return err
	}
	if out, err := s.phoneOut(ctx, id, "monkey", "-p", pkg, "-c", "android.intent.category.LAUNCHER", "1"); err != nil {
		return fmt.Errorf("打开失败：%v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// UninstallApp 卸载一个应用（当前用户）。
func (s *Service) UninstallApp(ctx context.Context, id, pkg string) error {
	if !pkgRe.MatchString(pkg) {
		return fmt.Errorf("%w：包名不对", ErrInvalid)
	}
	if err := s.requireRunning(ctx, id, true); err != nil {
		return err
	}
	out, err := s.phoneOut(ctx, id, "pm", "uninstall", "--user", "0", pkg)
	if err != nil || !strings.Contains(out, "Success") {
		return fmt.Errorf("卸载失败：%s", strings.TrimSpace(out+" "+errText(err)))
	}
	return nil
}
