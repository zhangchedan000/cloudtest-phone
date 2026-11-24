package phones

import (
	"strings"
	"testing"
)

func TestCodecsXML(t *testing.T) {
	orig := []byte(`<Decoders>
        <MediaCodec name="c2.rk.avc.decoder" type="video/avc">
            <Limit name="size" min="2x2" max="4096x2304" />
        </MediaCodec>
        <MediaCodec name="c2.rk.av1.decoder" type="video/av01">
        </MediaCodec>
    </Decoders>
    <Encoders>
        <MediaCodec name="c2.rk.avc.encoder" type="video/avc">
        </MediaCodec>
    </Encoders>`)
	real := []CodecInfo{
		{Name: "c2.mtk.avc.decoder", Canonical: "c2.mtk.avc.decoder", Type: "video/avc"},
		{Name: "OMX.MTK.VIDEO.DECODER.AVC", Canonical: "c2.mtk.avc.decoder", Type: "video/avc"},
		{Name: "c2.mtk.avc.encoder", Canonical: "c2.mtk.avc.encoder", Type: "video/avc", Encoder: true},
		{Name: "c2.mtk.hevc.decoder", Canonical: "c2.mtk.hevc.decoder", Type: "video/hevc"},
		{Name: "bad name<", Canonical: "bad name<", Type: "video/avc"},
	}
	out, n := codecsXML(orig, real)
	s := string(out)
	if n != 3 {
		t.Fatalf("应加 3 个别名，加了 %d：\n%s", n, s)
	}
	dec := s[strings.Index(s, "c2.rk.avc.decoder"):strings.Index(s, "c2.rk.av1.decoder")]
	if !strings.Contains(dec, `<Alias name="c2.mtk.avc.decoder" />`) || !strings.Contains(dec, `<Alias name="OMX.MTK.VIDEO.DECODER.AVC" />`) || strings.Contains(dec, "encoder\" />") {
		t.Fatalf("解码器别名不对：\n%s", dec)
	}
	enc := s[strings.Index(s, "c2.rk.avc.encoder"):]
	if !strings.Contains(enc, `<Alias name="c2.mtk.avc.encoder" />`) {
		t.Fatalf("编码器别名不对：\n%s", enc)
	}
	if strings.Contains(s, "bad name") {
		t.Fatal("不合法的名字不能写进去")
	}
}
