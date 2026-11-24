package phones

import (
	"bytes"
	"strings"
	"testing"
)

func TestShimConfig(t *testing.T) {
	lib := append(append([]byte("ELF....junk"), append([]byte("PPGLCFG1"), make([]byte, 32768-8)...)...), []byte("tail")...)
	g := &GPUInfo{Vendor: "Qualcomm", Renderer: "Adreno (TM) 740", Version: "OpenGL ES 3.2 V@0676.42", Extensions: "GL_OES_a GL_QCOM_b"}
	out, err := shimConfig(lib, g)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(lib) || !bytes.HasSuffix(out, []byte("tail")) {
		t.Fatal("长度 / 配置区外的内容变了")
	}
	i := bytes.Index(out, []byte("PPGLCFG1"))
	cfg := out[i+8:]
	if cfg[0] != 1 {
		t.Fatal("开关没打开")
	}
	f := strings.Split(string(cfg[1:]), "\x00")
	want := []string{"Qualcomm", "Adreno (TM) 740", "OpenGL ES 3.2 V@0676.42", "OpenGL ES GLSL ES 3.20", "GL_OES_a GL_QCOM_b"}
	for k, w := range want {
		if f[k] != w {
			t.Errorf("第 %d 项 %q，应该是 %q", k, f[k], w)
		}
	}
	// 扩展太长：截在完整的扩展上，不越界
	g.Extensions = strings.Repeat("GL_EXT_longname ", 5000)
	out, err = shimConfig(lib, g)
	if err != nil || len(out) != len(lib) || !bytes.HasSuffix(out, []byte("tail")) {
		t.Fatal("长扩展列表处理错了", err)
	}
	if _, err := shimConfig([]byte("no marker"), g); err == nil {
		t.Fatal("没有配置区应该报错")
	}
}

func TestShimEGLName(t *testing.T) {
	cases := []struct {
		m    Model
		want string
	}{
		{Model{GPU: &GPUInfo{Vendor: "Qualcomm", EGL: "adreno"}}, "adreno"},
		{Model{GPU: &GPUInfo{Vendor: "Qualcomm"}}, "adreno"},
		{Model{GPU: &GPUInfo{Vendor: "ARM", EGL: "meow"}, Hardware: "mt6989"}, "meow"},
		{Model{GPU: &GPUInfo{Vendor: "ARM", EGL: "mali"}, Hardware: "mt6835"}, "meow"}, // 不能盖板子的真驱动 libGLES_mali.so
		{Model{GPU: &GPUInfo{Vendor: "ARM", EGL: "mali"}, Hardware: "shiba", Values: map[string]any{"boardPlatform": "zuma"}}, "zuma"},
		{Model{GPU: &GPUInfo{Vendor: "Samsung"}, Hardware: "s5e9955", Values: map[string]any{"boardPlatform": "erd9955"}}, "erd9955"},
	}
	for _, c := range cases {
		if got := shimEGLName(&c.m); got != c.want {
			t.Errorf("%+v → %q，应该是 %q", c.m.GPU, got, c.want)
		}
	}
}

func TestShimVulkanName(t *testing.T) {
	cases := []struct {
		g    *GPUInfo
		want string
	}{
		{&GPUInfo{Vendor: "Qualcomm", Vulkan: "adreno"}, "adreno"},
		{&GPUInfo{Vendor: "Qualcomm"}, "adreno"},
		{&GPUInfo{Vendor: "ARM", Vulkan: "mali"}, "mali"},
		{&GPUInfo{Vendor: "ARM"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := shimVulkanName(&Model{GPU: c.g}); got != c.want {
			t.Errorf("%+v → %q，应该是 %q", c.g, got, c.want)
		}
	}
	// 一致档的联发科机型：egl 用真机的 meow（包着 mali，和真机一样）
	if got := shimEGLName(&Model{GPU: &GPUInfo{Vendor: "ARM", EGL: "meow"}, Hardware: "mt6895"}); got != "meow" {
		t.Errorf("一致档 → %q", got)
	}
}
