package phones

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 伪装档（2026-11-24）：真机显卡不是板子的 Mali-G610 的机型（高通 Adreno、别的 Mali、三星 Xclipse……）。
// 显卡驱动还是板子的，App 问显卡名 / 版本 / 扩展列表时由转接库 pp_glshim（host/image/glshim）照真机回答；
// 硬件名（ro.hardware=qcom 等）对应的显示 / 显存模块用镜像里现成的那份挂过去。都是开机前 docker 挂进去的：
// 开机后再挂来不及（zygote 开机就把显卡驱动预加载了，App 都从它继承）。
// 改不了的：Vulkan 显卡名、OpenCL、/dev/mali0、显卡核心数、渲染结果——深度检测会露馅，面板提示。

// GPUInfo 真机的显卡字符串（导入机型包时录的）。
type GPUInfo struct {
	Vendor     string `json:"vendor"`
	Renderer   string `json:"renderer"`
	Version    string `json:"version"`
	GLSL       string `json:"glsl,omitempty"`
	Extensions string `json:"extensions"`
	EGL        string `json:"egl,omitempty"`    // 真机 ro.hardware.egl
	Vulkan     string `json:"vulkan,omitempty"` // 真机 ro.hardware.vulkan
}

// Disguised 伪装档（显卡和板子不是一个）。
func (m *Model) Disguised() bool { return m.Tier == "disguised" }

var hwNameRe = regexp.MustCompile(`^[a-z0-9_]{2,32}$`)

// shimEGLName 转接库挂成哪个名字（安卓按 ro.hardware.egl 找 libGLES_<名>.so）：真机的名字；真机没设或者是 mali
// （和板子真驱动重名，不能盖）时，高通用 adreno、联发科用 meow（联发科真机就是 meow 包着 mali）、其余用平台名。
func shimEGLName(m *Model) string {
	if m.GPU == nil {
		return ""
	}
	e := strings.ToLower(m.GPU.EGL)
	if e != "" && e != "mali" && hwNameRe.MatchString(e) {
		return e
	}
	switch {
	case strings.Contains(strings.ToLower(m.GPU.Vendor), "qualcomm"):
		return "adreno"
	case strings.HasPrefix(strings.ToLower(m.Hardware), "mt"):
		return "meow"
	}
	if p, _ := m.Values["boardPlatform"].(string); hwNameRe.MatchString(strings.ToLower(p)) && strings.ToLower(p) != "mali" {
		return strings.ToLower(p)
	}
	return "meow"
}

// shimConfig 按 pp_glshim.c 的格式改写转接库里的配置区：标记 "PPGLCFG1"、开关 1、厂商、显卡名、版本、GLSL、扩展（各以 \0 结尾）。
func shimConfig(lib []byte, g *GPUInfo) ([]byte, error) {
	const size = 32768
	i := bytes.Index(lib, []byte("PPGLCFG1"))
	if i < 0 || i+size > len(lib) {
		return nil, fmt.Errorf("转接库里找不到配置区")
	}
	glsl := g.GLSL
	if glsl == "" {
		if m := regexp.MustCompile(`OpenGL ES (\d)\.(\d)`).FindStringSubmatch(g.Version); m != nil && m[1] >= "3" {
			glsl = "OpenGL ES GLSL ES " + m[1] + "." + m[2] + "0"
		}
	}
	ext := g.Extensions
	fixed := len(g.Vendor) + len(g.Renderer) + len(g.Version) + len(glsl) + 5
	if fixed+len(ext) > size-9-1 { // 扩展列表太长就截到最后一个完整的扩展
		ext = ext[:size-9-1-fixed]
		if k := strings.LastIndexByte(ext, ' '); k > 0 {
			ext = ext[:k]
		}
	}
	cfg := []byte{1}
	for _, s := range []string{g.Vendor, g.Renderer, g.Version, glsl, ext} {
		cfg = append(append(cfg, s...), 0)
	}
	out := append([]byte{}, lib...)
	area := out[i+8 : i+size]
	for k := range area {
		area[k] = 0
	}
	copy(area, cfg)
	return out, nil
}

// disguiseBinds 伪装档开机前要挂的东西和要加的属性。返回的 hw 非空时替代机型芯片名当 ro.hardware。
// have = 镜像带了驱动的芯片名（镜像标签 prism.hw），没有（老镜像）就什么都不做。
func (s *Service) disguiseBinds(ctx context.Context, id, image string, m *Model, have []string) (binds, props []string, hw string) {
	if m == nil || !m.Disguised() || len(have) == 0 {
		return nil, nil, ""
	}
	// 1. 硬件名：真机的 ro.hardware（qcom、mt6989……）镜像里没有对应的显示 / 显存模块，就把镜像里现成的一份挂成这个名字
	if want := strings.ToLower(m.Hardware); hwNameRe.MatchString(want) && want != "redroid" {
		ok := false
		for _, h := range have {
			ok = ok || h == want
		}
		if ok {
			hw = want
		} else if mods, err := s.hwModules(ctx, image, have[0]); err != nil {
			log.Printf("手机 %s：取显示模块失败，硬件名照旧：%v", id, err)
		} else {
			for rel, src := range mods { // rel = lib64/hw/gralloc
				binds = append(binds, src+":/vendor/"+rel+"."+want+".so:ro")
			}
			hw = want
		}
	}
	// 2. 显卡字符串：转接库挂成真机的 egl 名字，ro.hardware.egl 指过去
	if m.GPU != nil && m.GPU.Renderer != "" && s.cfg.Tools != nil {
		if lib, err := fs.ReadFile(s.cfg.Tools, "libGLES_pp.so"); err == nil {
			if patched, err := shimConfig(lib, m.GPU); err == nil {
				dir := filepath.Join(s.phoneDir(id), "glshim")
				_ = os.MkdirAll(dir, 0o755)
				p := filepath.Join(dir, "libGLES.so")
				if os.WriteFile(p, patched, 0o644) == nil {
					name := shimEGLName(m)
					binds = append(binds, p+":/vendor/lib64/egl/libGLES_"+name+".so:ro")
					props = append(props, "ro.hardware.egl="+name)
				}
			}
		}
	}
	return binds, props, hw
}

// hwModules 从镜像里取一份显示 / 显存模块（按芯片名 from 的那份），缓存在数据目录里。返回 「lib64/hw/gralloc」→ 宿主机文件。
func (s *Service) hwModules(ctx context.Context, image, from string) (map[string]string, error) {
	idOut, err := s.run.Run(ctx, "docker", "image", "inspect", "-f", "{{.Id}}", image)
	if err != nil {
		return nil, err
	}
	key := strings.TrimPrefix(strings.TrimSpace(idOut), "sha256:")
	if len(key) > 16 {
		key = key[:16]
	}
	dir := filepath.Join(s.cfg.DataDir, "hwmods", key)
	want := map[string]string{}
	for _, arch := range []string{"lib64", "lib"} {
		for _, mod := range []string{"gralloc", "hwcomposer"} {
			want[arch+"/hw/"+mod] = filepath.Join(dir, arch, mod+".so")
		}
	}
	missing := false
	for _, p := range want {
		if _, err := os.Stat(p); err != nil {
			missing = true
		}
	}
	if missing && !fileExists(filepath.Join(dir, ".done")) {
		cid, err := s.run.Run(ctx, "docker", "create", image, "/system/bin/sh")
		if err != nil {
			return nil, err
		}
		cid = strings.TrimSpace(cid)
		defer s.run.Run(ctx, "docker", "rm", cid) //nolint:errcheck
		for rel, p := range want {
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			src := "/vendor/" + rel + "." + from + ".so"
			if _, err := s.run.Run(ctx, "docker", "cp", cid+":"+src, p); err != nil {
				_ = os.Remove(p) // 镜像里没有这一份（比如没有 32 位）：不挂
			}
		}
		_ = os.WriteFile(filepath.Join(dir, ".done"), nil, 0o644)
	}
	out := map[string]string{}
	for rel, p := range want {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			out[rel] = p
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("镜像 %s 里没有 %s 的显示模块", image, from)
	}
	return out, nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
