package phones

import (
	"strings"
	"testing"
)

const emuBack = `{
 "android.lens.facing": ["BACK"],
 "android.sensor.info.pixelArraySize": ["1856", "1392"],
 "android.sensor.info.activeArraySize": ["0", "0", "1856", "1392"],
 "android.sensor.orientation": ["90"],
 "android.flash.info.available": ["TRUE"],
 "android.scaler.availableStreamConfigurations": ["35", "1856", "1392", "INPUT", "33", "1856", "1392", "OUTPUT", "33", "1280", "720", "OUTPUT", "32", "1856", "1392", "OUTPUT",],
 "android.scaler.availableMinFrameDurations": ["33", "1856", "1392", "33331760", "33", "1280", "720", "33331760",],
 "android.scaler.availableStallDurations": ["33", "1856", "1392", "33331760",],
}`

func TestCameraConfig(t *testing.T) {
	base, err := parseCameraJSON([]byte(emuBack))
	if err != nil {
		t.Fatal(err)
	}
	front := cameraConfig(base, CameraInfo{Facing: "front", Width: 1600, Height: 1200, FocalLengths: []float64{3.64}, Apertures: []float64{2.45}, Orientation: 270})
	if front["android.lens.facing"][0] != "FRONT" || front["android.flash.info.available"][0] != "FALSE" || front["android.sensor.orientation"][0] != "270" {
		t.Fatalf("前摄朝向 / 闪光灯 / 方向不对：%v", front)
	}
	if strings.Join(front["android.sensor.info.pixelArraySize"], "x") != "1600x1200" || front["android.lens.info.availableFocalLengths"][0] != "3.64" {
		t.Fatalf("尺寸 / 焦距不对")
	}
	sc := strings.Join(front["android.scaler.availableStreamConfigurations"], ",")
	if sc != "33,1280,720,OUTPUT,32,1600,1200,OUTPUT,33,1600,1200,OUTPUT,35,1600,1200,OUTPUT,34,1600,1200,OUTPUT" {
		t.Fatalf("尺寸列表不对：%s", sc)
	}
	back := cameraConfig(base, CameraInfo{Facing: "back", Width: 4096, Height: 3072})
	if st := strings.Join(back["android.scaler.availableStallDurations"], ","); st != "33,1856,1392,33331760,33,4096,3072,81788928" {
		t.Fatalf("拍照停顿时间要按像素算：%v", st)
	}
	if base["android.sensor.info.pixelArraySize"][0] != "1856" {
		t.Fatal("不能改到底本")
	}
}

func TestPickCameraDefaults(t *testing.T) {
	m := &Model{Cameras: []CameraInfo{{Facing: "back", Width: 4000, Height: 3000}}}
	if c := pickCamera(m, "back"); c.Orientation != 90 || c.Width != 4000 {
		t.Fatalf("后摄：%+v", c)
	}
	if c := pickCamera(m, "front"); c.Width != 3264 {
		t.Fatalf("没录前摄用一般值：%+v", c)
	}
}
