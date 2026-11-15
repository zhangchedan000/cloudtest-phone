package api

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
)

// 截图缩小成 JPEG（2026-11-15）：原图是手机分辨率的 PNG（1080×2460 约 1–2 MB），国内直连阿什本只有几百 Kbit/s，
// 打开详情页要等几十秒。缩到页面显示的宽度、存成 JPEG 后只有一两百 KB。

func pngConfig(data []byte) (image.Config, error) { return png.DecodeConfig(bytes.NewReader(data)) }

// shrinkToJPEG 把 PNG 截图缩到宽度不超过 maxW（按整数倍取平均，清楚又快），存成 JPEG。返回 JPEG 和原图尺寸。
func shrinkToJPEG(pngData []byte, maxW, quality int) ([]byte, int, int, error) {
	src, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, 0, 0, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	f := 1
	if maxW > 0 && w > maxW {
		f = (w + maxW - 1) / maxW
	}
	dw, dh := w/f, h/f
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	pix := func(x, y int) (r, g, bl uint32) {
		switch im := src.(type) {
		case *image.NRGBA:
			i := im.PixOffset(b.Min.X+x, b.Min.Y+y)
			return uint32(im.Pix[i]), uint32(im.Pix[i+1]), uint32(im.Pix[i+2])
		case *image.RGBA:
			i := im.PixOffset(b.Min.X+x, b.Min.Y+y)
			return uint32(im.Pix[i]), uint32(im.Pix[i+1]), uint32(im.Pix[i+2])
		}
		r16, g16, b16, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return r16 >> 8, g16 >> 8, b16 >> 8
	}
	n := uint32(f * f)
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sr, sg, sb uint32
			for yy := 0; yy < f; yy++ {
				for xx := 0; xx < f; xx++ {
					r, g, bl := pix(x*f+xx, y*f+yy)
					sr, sg, sb = sr+r, sg+g, sb+bl
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(sr/n), uint8(sg/n), uint8(sb/n), 255
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, err
	}
	return out.Bytes(), w, h, nil
}
