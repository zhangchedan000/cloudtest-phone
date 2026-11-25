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
	"sync"
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
	if m == nil || len(have) == 0 || (!m.Disguised() && (m.GPU == nil || m.GPU.Renderer == "")) {
		return nil, nil, ""
	}
	// 1. 硬件名（只有伪装档：一致档的芯片名镜像里本来就有）：真机的 ro.hardware（qcom、mt6989……）镜像里没有对应的显示 / 显存模块，就把镜像里现成的一份挂成这个名字
	if want := strings.ToLower(m.Hardware); m.Disguised() && hwNameRe.MatchString(want) && want != "redroid" {
		ok := false
		for _, h := range have {
			ok = ok || h == want
		}
		if ok {
			hw = want
		} else if mods, err := s.hwModules(ctx, image, map[string]string{
			"lib64/hw/gralloc": "/vendor/lib64/hw/gralloc." + have[0] + ".so", "lib64/hw/hwcomposer": "/vendor/lib64/hw/hwcomposer." + have[0] + ".so",
			"lib/hw/gralloc": "/vendor/lib/hw/gralloc." + have[0] + ".so", "lib/hw/hwcomposer": "/vendor/lib/hw/hwcomposer." + have[0] + ".so",
		}); err != nil {
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
	// 3. Vulkan（超越魔云腾——它的 Vulkan 没伪装）：转接库挂成真机的 vulkan 名字，ro.hardware.vulkan 指过去。
	// 转接库 dlopen 真 Mali 驱动做实际渲染，只拦 vkGetPhysicalDeviceProperties(2) 把 deviceName/vendorID 换成真机的。
	// 有真机显卡字符串（伪装档）才上转接库；没有就退回「只改名字、不伪装显卡名」（和以前一样）。标签由小助手补 same_process_hal_file（phonecmd/label.go）
	if vk := shimVulkanName(m); vk != "" && vk != "mali" {
		done := false
		if m.GPU != nil && m.GPU.Renderer != "" && s.cfg.Tools != nil {
			// 真 Mali 驱动挂到隐藏点文件 .vkreal.so（shim dlopen 它做实际渲染；小助手会删掉可见的 vulkan.mali.so）
			if real, err := s.hwModules(ctx, image, map[string]string{"lib64/hw/vulkan": "/vendor/lib64/hw/vulkan.mali.so"}); err == nil {
				if lib, err := fs.ReadFile(s.cfg.Tools, "libvulkan_pp.so"); err == nil {
					if patched, err := vkShimConfig(lib, m.GPU); err == nil {
						dir := filepath.Join(s.phoneDir(id), "vkshim")
						_ = os.MkdirAll(dir, 0o755)
						p := filepath.Join(dir, "libvulkan.so")
						if os.WriteFile(p, patched, 0o644) == nil {
							binds = append(binds, real["lib64/hw/vulkan"]+":/vendor/lib64/hw/.vkreal.so:ro")
							binds = append(binds, p+":/vendor/lib64/hw/vulkan."+vk+".so:ro")
							props = append(props, "ro.hardware.vulkan="+vk)
							done = true
						}
					}
				}
			}
		}
		if !done { // 退路：没转接库 / 没显卡数据，只把真驱动挂成真机的名字（显卡名还是 Mali）
			if mods, err := s.hwModules(ctx, image, map[string]string{"lib64/hw/vulkan": "/vendor/lib64/hw/vulkan.mali.so"}); err == nil {
				binds = append(binds, mods["lib64/hw/vulkan"]+":/vendor/lib64/hw/vulkan."+vk+".so:ro")
				props = append(props, "ro.hardware.vulkan="+vk)
			}
		}
	}
	return binds, props, hw
}

// vkVendorID 真机显卡厂商 → Vulkan vendorID（PCI ID）。认不出返回 0（不改）。
func vkVendorID(vendor string) uint32 {
	switch v := strings.ToLower(vendor); {
	case strings.Contains(v, "qualcomm"):
		return 0x5143
	case strings.Contains(v, "arm"):
		return 0x13B5
	case strings.Contains(v, "samsung"):
		return 0x144D
	case strings.Contains(v, "imagination") || strings.Contains(v, "powervr"):
		return 0x1010
	case strings.Contains(v, "nvidia"):
		return 0x10DE
	case strings.Contains(v, "intel"):
		return 0x8086
	case strings.Contains(v, "amd") || strings.Contains(v, "advanced micro"):
		return 0x1002
	}
	return 0
}

// vkShimConfig 按 pp_vkshim.c 的格式改写转接库配置区：标记 "PPVKCFG1"、开关 1、deviceName(偏移 9)、vendorID(偏移 280，小端)。
func vkShimConfig(lib []byte, g *GPUInfo) ([]byte, error) {
	const size = 320
	i := bytes.Index(lib, []byte("PPVKCFG1"))
	if i < 0 || i+size > len(lib) {
		return nil, fmt.Errorf("Vulkan 转接库里找不到配置区")
	}
	name := g.Renderer // Vulkan deviceName 用 GL renderer（Adreno (TM) 830 这类，和真机 Vulkan 一致）
	if len(name) > 255 {
		name = name[:255]
	}
	out := append([]byte{}, lib...)
	area := out[i : i+size]
	for k := 8; k < size; k++ {
		area[k] = 0
	}
	area[8] = 1 // enable
	copy(area[9:], name)
	vid := vkVendorID(g.Vendor)
	area[280], area[281], area[282], area[283] = byte(vid), byte(vid>>8), byte(vid>>16), byte(vid>>24)
	return out, nil
}

// shimVulkanName 真机的 ro.hardware.vulkan；真机没设的高通机型用 adreno（总比在高通手机上写 mali 强），其余照旧 mali。
func shimVulkanName(m *Model) string {
	if m.GPU == nil {
		return ""
	}
	if v := strings.ToLower(m.GPU.Vulkan); hwNameRe.MatchString(v) {
		return v
	}
	if strings.Contains(strings.ToLower(m.GPU.Vendor), "qualcomm") {
		return "adreno"
	}
	return ""
}

// hwModules 从镜像里取几个文件（files：缓存名 → 镜像里的路径），缓存在数据目录里。返回 缓存名 → 宿主机文件（镜像里没有的不返回）。
// hwModulesMu 几台手机同时开机会一起取同一个缓存文件（2026-11-24 实测：一台取到、另一台写坏后记成「镜像里没有」，
// 那台就没挂上 Vulkan 入口模块），一次只让一个取
var hwModulesMu sync.Mutex

func (s *Service) hwModules(ctx context.Context, image string, files map[string]string) (map[string]string, error) {
	hwModulesMu.Lock()
	defer hwModulesMu.Unlock()
	dir, err := s.imageCacheDir(ctx, image)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	var missing []string
	for name := range files {
		p := filepath.Join(dir, name+".so")
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			out[name] = p
		} else if !fileExists(p + ".none") {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		cid, err := s.run.Run(ctx, "docker", "create", image, "/system/bin/sh")
		if err != nil {
			return nil, err
		}
		cid = strings.TrimSpace(cid)
		defer s.run.Run(ctx, "docker", "rm", cid) //nolint:errcheck
		for _, name := range missing {
			p := filepath.Join(dir, name+".so")
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			if _, err := s.run.Run(ctx, "docker", "cp", cid+":"+files[name], p); err == nil {
				if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
					out[name] = p
					continue
				}
			}
			_ = os.Remove(p)
			_ = os.WriteFile(p+".none", nil, 0o644) // 镜像里没有（比如没有 32 位）：记下来，下次不再取
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("镜像 %s 里没有要的模块", image)
	}
	return out, nil
}

// imageCacheDir 从镜像里取出来的文件的缓存目录（按镜像 ID 分，镜像换了自动重取）。
func (s *Service) imageCacheDir(ctx context.Context, image string) (string, error) {
	idOut, err := s.run.Run(ctx, "docker", "image", "inspect", "-f", "{{.Id}}", image)
	if err != nil {
		return "", err
	}
	key := strings.TrimPrefix(strings.TrimSpace(idOut), "sha256:")
	if len(key) > 16 {
		key = key[:16]
	}
	return filepath.Join(s.cfg.DataDir, "hwmods", key), nil
}

// boardPlatform 机型的平台名（ro.board.platform），不像样的不设。
func boardPlatform(byKey map[string]any) string {
	p := strings.ToLower(strings.TrimSpace(str(byKey["boardPlatform"])))
	if !hwNameRe.MatchString(p) || p == "redroid" || strings.HasPrefix(p, "rk3") {
		return ""
	}
	return p
}

var boardPropKeys = map[string]bool{"ro.product.board": true, "ro.bootloader": true, "ro.baseband": true, "ro.boot.baseband": true, "ro.boot.bootloader": true}
var boardPropVal = regexp.MustCompile(`^[\w.\-]{1,64}$`)

// boardProps 开机参数里的 ro.product.board 等：机型录了真机的就用真机的；没录的 ro.product.board 用平台名（高通真机两个一样）。
func boardProps(m *Model, byKey map[string]any) []string {
	got := map[string]string{}
	if m != nil {
		for k, v := range m.BoardProps {
			if boardPropKeys[k] && boardPropVal.MatchString(v) {
				got[k] = v
			}
		}
	}
	if got["ro.product.board"] == "" {
		if pl := boardPlatform(byKey); pl != "" {
			got["ro.product.board"] = pl
		}
	}
	var out []string
	for _, k := range []string{"ro.product.board", "ro.bootloader", "ro.boot.bootloader", "ro.baseband", "ro.boot.baseband"} {
		if got[k] != "" {
			out = append(out, k+"="+got[k])
		}
	}
	return out
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
