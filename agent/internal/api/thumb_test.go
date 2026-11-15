package api

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestShrinkToJPEG(t *testing.T) {
	// 108×246 的「截图」：上半红、下半蓝
	src := image.NewNRGBA(image.Rect(0, 0, 108, 246))
	for y := 0; y < 246; y++ {
		for x := 0; x < 108; x++ {
			c := color.NRGBA{200, 0, 0, 255}
			if y >= 123 {
				c = color.NRGBA{0, 0, 200, 255}
			}
			src.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, src)

	jpg, w, h, err := shrinkToJPEG(buf.Bytes(), 54, 75)
	if err != nil || w != 108 || h != 246 {
		t.Fatalf("原图尺寸不对：%d×%d %v", w, h, err)
	}
	out, err := jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	if b := out.Bounds(); b.Dx() != 54 || b.Dy() != 123 {
		t.Fatalf("缩小后尺寸不对：%v", b)
	}
	r, _, bl, _ := out.At(10, 10).RGBA()
	r2, _, bl2, _ := out.At(10, 110).RGBA()
	if r>>8 < 150 || bl>>8 > 60 || bl2>>8 < 150 || r2>>8 > 60 {
		t.Fatalf("颜色不对：上 r=%d b=%d，下 r=%d b=%d", r>>8, bl>>8, r2>>8, bl2>>8)
	}
	if len(jpg) >= buf.Len()*4 {
		t.Fatalf("JPEG 不应比原图大很多：%d vs %d", len(jpg), buf.Len())
	}
	// 本来就比要求的小：不缩
	if _, _, _, err := shrinkToJPEG(buf.Bytes(), 500, 75); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := shrinkToJPEG([]byte("不是图片"), 54, 75); err == nil {
		t.Fatal("坏数据应报错")
	}
}
