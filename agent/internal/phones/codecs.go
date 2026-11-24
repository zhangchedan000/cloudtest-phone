package phones

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 编解码器名字照真机（2026-11-24，学魔云腾：机型包录了真机的编解码器列表）。
// 手机里的硬件编解码是板子的瑞芯微（c2.rk.avc.decoder……），App 列编解码器一看就是瑞芯微，不是天玑 / 骁龙。
// 组件名是瑞芯微的服务给的，改不了；能做的是在编解码器配置（media_codecs_c2_base.xml，镜像里本来就有的文件）里
// 给每个瑞芯微编解码器加上真机同类型硬件编解码器的名字当别名（c2.mtk.avc.decoder、OMX.MTK.VIDEO.DECODER.AVC……）：
// App 列出来真机的名字都在，按真机名字创建也能用（实际干活的还是瑞芯微的）。c2.rk.* 本身还在列表里，去不掉。

// CodecInfo 真机一个硬件编解码器（别名的 Canonical 是它指向的那个）。
type CodecInfo struct {
	Name      string `json:"name"`
	Canonical string `json:"canonical"`
	Type      string `json:"type"`
	Encoder   bool   `json:"encoder"`
}

const codecFile = "/vendor/etc/media_codecs_c2_base.xml"

var (
	codecOpenRe = regexp.MustCompile(`<MediaCodec\s+name="([^"]+)"\s+type="([^"]+)"[^>]*?>`)
	codecNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,80}$`)
)

// codecAliases 给一个板子编解码器（类型、编 / 解码）配的真机名字：同类型的真机硬件编解码器，连同它们的别名，按真机顺序。
func codecAliases(real []CodecInfo, typ string, enc bool) []string {
	canon := map[string]bool{}
	for _, c := range real {
		if c.Type == typ && c.Encoder == enc {
			canon[c.Canonical] = true
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range real {
		if canon[c.Canonical] && c.Type == typ && c.Encoder == enc && !seen[c.Name] && codecNameRe.MatchString(c.Name) {
			seen[c.Name] = true
			out = append(out, c.Name)
		}
	}
	return out
}

// codecsXML 在每个板子编解码器的开始标签后面插入别名。
func codecsXML(orig []byte, real []CodecInfo) ([]byte, int) {
	used := map[string]bool{}
	n := 0
	out := codecOpenRe.ReplaceAllStringFunc(string(orig), func(tag string) string {
		m := codecOpenRe.FindStringSubmatch(tag)
		if strings.HasSuffix(tag, "/>") || strings.HasPrefix(m[1], "c2.android.") || strings.HasPrefix(m[1], "OMX.google.") {
			return tag
		}
		var b strings.Builder
		b.WriteString(tag)
		for _, a := range codecAliases(real, m[2], strings.Contains(m[1], "encoder")) {
			if used[a] || a == m[1] {
				continue // 一个名字只能给一个
			}
			used[a] = true
			n++
			fmt.Fprintf(&b, "\n            <Alias name=\"%s\" />", a)
		}
		return b.String()
	})
	return []byte(out), n
}

// codecBinds 机型录了真机编解码器时，开机前挂的编解码器配置。
func (s *Service) codecBinds(ctx context.Context, id, image string, m *Model) []string {
	if m == nil || len(m.Codecs) == 0 {
		return nil
	}
	mods, err := s.hwModules(ctx, image, map[string]string{"etc/media_codecs_c2_base.xml": codecFile})
	if err != nil {
		return nil // 镜像里没有（没有硬件编解码的镜像）
	}
	orig, err := os.ReadFile(mods["etc/media_codecs_c2_base.xml"])
	if err != nil {
		return nil
	}
	out, n := codecsXML(orig, m.Codecs)
	if n == 0 {
		return nil
	}
	dir := filepath.Join(s.phoneDir(id), "codecs")
	_ = os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, filepath.Base(codecFile))
	if err := os.WriteFile(p, out, 0o644); err != nil {
		log.Printf("手机 %s：写编解码器配置失败：%v", id, err)
		return nil
	}
	return []string{p + ":" + codecFile + ":ro"}
}
