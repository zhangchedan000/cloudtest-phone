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
	"regexp"
	"sort"
	"strings"
)

// 应用列表（2026-11-26 用户要求，对齐魔云腾）：列出手机里有桌面图标的 App，显示「图标 + 名字」，一键打开 / 卸载。
//
// 名字和图标是 APK 里的资源，shell 命令取不到；和剪贴板一个路子：在容器里用 app_process 跑一个小程序
// （tools/applist/Apps.java，编译成 pp-apps.dex 内嵌在这里），反射造一个 system Context 拿到真正的
// PackageManager，queryIntentActivities 列启动项、loadLabel 取名字、loadIcon 把图标渲染成 PNG。做法参考 scrcpy。
//
// 打开 App 用 `am start -n 包名/Activity`（am 是 cmd 封装，不需要 app_process）；小程序顺带把启动 Activity 一起给出来。
// 卸载用 `cmd package uninstall`。这两个都不碰 app_process，开了 SELinux 的手机走命令小助手也没问题。

//go:embed pp-apps.dex
var appsDex []byte

// appsWrapper 和 clipWrapper 一样补环境变量；踩坑 #171：用 nsenter 切到 1 号进程的正式挂载视图再跑 app_process。
const appsWrapper = `for f in /init.environ.rc /data/system/environ/classpath; do
  while read -r k n v; do [ "$k" = export ] && export "$n=$v"; done < "$f"
done
export CLASSPATH="$PP_APPS_DEX"
NS=
command -v nsenter >/dev/null 2>&1 && NS="nsenter --mount=/proc/1/ns/mnt --"
exec $NS app_process / Apps "$@"
`

var pkgRe = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.]{1,254}$`)

// AppInfo 一个应用。
type AppInfo struct {
	Package  string `json:"package"`
	Name     string `json:"name"`               // 显示名字（取不到就退回包名）
	Activity string `json:"activity,omitempty"` // 启动 Activity，打开用
	User     bool   `json:"user"`               // 用户自己装的（非系统预装）
	Icon     string `json:"icon,omitempty"`     // data:image/png;base64,... 的图标
}

// appsPaths 容器里的小程序和启动脚本路径；文件名带内容哈希，升级后自动换新的。
func appsPaths() (dex, sh string) {
	sum := sha256.Sum256(append(append([]byte{}, appsDex...), appsWrapper...))
	tag := hex.EncodeToString(sum[:4])
	return "/data/local/tmp/pp-apps-" + tag + ".dex", "/data/local/tmp/pp-apps-" + tag + ".sh"
}

// ensureAppsTool 小程序不在手机里就放进去（重置 / 刷机会清空 /data/local/tmp）。
func (s *Service) ensureAppsTool(ctx context.Context, name string) (string, error) {
	dex, sh := appsPaths()
	if _, err := s.run.Run(ctx, "docker", "exec", name, "test", "-f", sh); err == nil {
		return sh, nil
	}
	dir := filepath.Join(s.cfg.DataDir, "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for remote, content := range map[string][]byte{dex: appsDex, sh: []byte(appsWrapper)} {
		f, err := os.CreateTemp(dir, "apps-*")
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
			return "", fmt.Errorf("应用列表工具传进手机失败：%v", werr)
		}
	}
	_, _ = s.run.Run(ctx, "docker", "exec", name, "chmod", "644", dex, sh)
	return sh, nil
}

// Apps 列出手机里有桌面图标的应用（可启动的），用户自己装的排前面。icons=true 时连图标 PNG 一起取（慢一点）。
func (s *Service) Apps(ctx context.Context, id string, icons bool) ([]AppInfo, error) {
	if err := s.requireRunning(ctx, id, true); err != nil {
		return nil, err
	}
	name := containerName(id)
	sh, err := s.ensureAppsTool(ctx, name)
	if err != nil {
		return nil, err
	}
	dex, _ := appsPaths()
	args := []string{"exec", "-e", "PP_APPS_DEX=" + dex, name, "sh", sh}
	if icons {
		args = append(args, "icon=96")
	}
	out, err := s.run.Run(ctx, "docker", args...)
	if err != nil {
		return nil, fmt.Errorf("列应用失败：%v %s", err, lastLine(out))
	}
	var list []AppInfo
	seen := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "error ") {
			return nil, fmt.Errorf("列应用失败：%s", strings.TrimPrefix(l, "error "))
		}
		f := strings.Split(l, "\t")
		if len(f) < 4 || !pkgRe.MatchString(f[0]) || seen[f[0]] {
			continue // app_process 开头可能打别的行，按「包名 + 制表符分隔」认数据行
		}
		seen[f[0]] = true
		label := f[0]
		if b, err := base64.StdEncoding.DecodeString(f[3]); err == nil && len(b) > 0 {
			label = string(b)
		}
		info := AppInfo{Package: f[0], Activity: f[1], Name: label, User: f[2] == "0"}
		if len(f) >= 5 && f[4] != "" {
			info.Icon = "data:image/png;base64," + f[4]
		}
		list = append(list, info)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].User != list[j].User {
			return list[i].User // 用户装的排前
		}
		return list[i].Name < list[j].Name
	})
	return list, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// LaunchApp 打开一个应用。activity 是启动 Activity（应用列表里给的）；用 am start（cmd 封装），不碰 app_process。
func (s *Service) LaunchApp(ctx context.Context, id, pkg, activity string) error {
	if !pkgRe.MatchString(pkg) {
		return fmt.Errorf("%w：包名不对", ErrInvalid)
	}
	if err := s.requireRunning(ctx, id, true); err != nil {
		return err
	}
	comp := pkg
	if activity != "" {
		a := activity
		if !strings.Contains(a, ".") {
			a = pkg + a // 短名（.Activity）补上包名
		} else if strings.HasPrefix(a, ".") {
			a = pkg + a
		}
		comp = pkg + "/" + a
	}
	args := []string{"exec", containerName(id), "am", "start"}
	if activity != "" {
		args = append(args, "-n", comp)
	} else {
		args = append(args, "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LAUNCHER", comp)
	}
	if out, err := s.run.Run(ctx, "docker", args...); err != nil || strings.Contains(out, "Error:") {
		return fmt.Errorf("打开失败：%v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// UninstallApp 卸载一个应用（当前用户）。用 cmd package（不碰 app_process）。
func (s *Service) UninstallApp(ctx context.Context, id, pkg string) error {
	if !pkgRe.MatchString(pkg) {
		return fmt.Errorf("%w：包名不对", ErrInvalid)
	}
	if err := s.requireRunning(ctx, id, true); err != nil {
		return err
	}
	out, err := s.run.Run(ctx, "docker", "exec", containerName(id), "cmd", "package", "uninstall", "--user", "0", pkg)
	if err != nil || !strings.Contains(out, "Success") {
		return fmt.Errorf("卸载失败：%s", strings.TrimSpace(out+" "+errText(err)))
	}
	return nil
}
