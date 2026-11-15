package phones

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 预装应用：把 APK 放进 DataDir/preinstall/（阿什本：/opt/prism-phone/data/preinstall/），
// 每台手机开机后自动装上（新建、一键重置、刷机之后的第一次开机都会装）。
//
// 装过的记在手机里的 /data/local/pp-preinstalled（一行一个「文件名 大小」）：
//   - 重置 / 刷机会清空 /data，记录跟着没了，所以会重新装；
//   - 用户自己在手机里卸载了，不会被装回来；
//   - 目录里换了新版本的 APK（文件名或大小变了），下次开机会装新版本（覆盖升级）。
// 2026-11-15 用户要求：每台都预装 Chrome（纯净版安卓没有浏览器，手机里没法自己下载 APK）。

const preinstallMarker = "/data/local/pp-preinstalled"

func (s *Service) preinstallDir() string { return filepath.Join(s.cfg.DataDir, "preinstall") }

// preinstallAPKs 目录里要预装的 APK（按文件名排序），每个带「文件名 大小」的记录键。
func (s *Service) preinstallAPKs() ([]string, map[string]string, error) {
	entries, err := os.ReadDir(s.preinstallDir())
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var files []string
	keys := map[string]string{}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.EqualFold(filepath.Ext(e.Name()), ".apk") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, e.Name())
		keys[e.Name()] = fmt.Sprintf("%s %d", e.Name(), info.Size())
	}
	sort.Strings(files)
	return files, keys, nil
}

// preinstall 在已开机的手机上装还没装过的预装 APK。返回装上的文件名和问题（问题不影响开机）。
func (s *Service) preinstall(ctx context.Context, id string) (installed, problems []string) {
	files, keys, err := s.preinstallAPKs()
	if err != nil {
		return nil, []string{"读预装目录失败：" + err.Error()}
	}
	if len(files) == 0 {
		return nil, nil
	}
	name := containerName(id)
	done, _ := s.run.Run(ctx, "docker", "exec", name, "cat", preinstallMarker) // 没有记录文件 = 一个都没装过
	have := map[string]bool{}
	for _, line := range strings.Split(done, "\n") {
		have[strings.TrimSpace(line)] = true
	}
	const remote = "/data/local/tmp/pp-preinstall.apk"
	for _, f := range files {
		key := keys[f]
		if have[key] {
			continue
		}
		local := filepath.Join(s.preinstallDir(), f)
		if out, err := s.run.Run(ctx, "docker", "cp", local, name+":"+remote); err != nil {
			problems = append(problems, fmt.Sprintf("预装 %s：传进手机失败 %v %s", f, err, strings.TrimSpace(out)))
			continue
		}
		out, err := s.run.Run(ctx, "docker", "exec", name, "pm", "install", "-r", remote)
		_, _ = s.run.Run(ctx, "docker", "exec", name, "rm", "-f", remote)
		if err != nil || !strings.Contains(out, "Success") {
			problems = append(problems, fmt.Sprintf("预装 %s 失败：%s", f, strings.TrimSpace(out+" "+errText(err))))
			continue
		}
		_, _ = s.run.Run(ctx, "docker", "exec", name, "sh", "-c", "echo "+shellQuote(key)+" >> "+preinstallMarker)
		installed = append(installed, f)
	}
	return installed, problems
}
