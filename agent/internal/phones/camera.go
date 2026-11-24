package phones

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// 摄像头参数照真机（踩坑 #152，学魔云腾：它的机型包录了真机摄像头的朝向、焦距、光圈、最大分辨率，
// 有的包干脆是整套 CameraCharacteristics，格式和谷歌模拟摄像头的配置文件一模一样）。
// 镜像补了模拟器的模拟摄像头服务（host/image/add-camera.sh，标签 prism.camera=1），它按
// /vendor/etc/config/emu_camera_{back,front}.json 建摄像头、自己合成画面。开机前按机型生成这两个文件挂上去：
// 以模拟器后摄那份（单个摄像头）为底，改成真机的朝向、焦距、光圈、传感器尺寸、方向、最大分辨率。
// 前摄也用后摄的底（模拟器自带的前摄是三合一的超高像素逻辑摄像头，真机前摄没有这样的）。
// 画面是合成的（模拟器的测试场景），不是真的拍到东西。

// CameraInfo 真机一个摄像头（机型包导入的）。
type CameraInfo struct {
	Facing       string    `json:"facing"` // back / front
	Width        int       `json:"width"`  // 传感器像素（最大分辨率）
	Height       int       `json:"height"`
	FocalLengths []float64 `json:"focalLengths,omitempty"`
	Apertures    []float64 `json:"apertures,omitempty"`
	Orientation  int       `json:"orientation,omitempty"`
	PhysicalSize []float64 `json:"physicalSize,omitempty"` // 毫米
}

// 没录摄像头的机型用的一般值（常见 1200 万 / 800 万）
var defaultCameras = map[string]CameraInfo{
	"back":  {Facing: "back", Width: 4000, Height: 3000, FocalLengths: []float64{5.43}, Apertures: []float64{1.8}, Orientation: 90},
	"front": {Facing: "front", Width: 3264, Height: 2448, FocalLengths: []float64{3.05}, Apertures: []float64{2.2}, Orientation: 270},
}

var trailingComma = regexp.MustCompile(`,\s*([\]}])`)

// parseCameraJSON 模拟器的配置文件（末尾多逗号，标准 JSON 解析器不认）。数组的取第一个。
func parseCameraJSON(b []byte) (map[string][]string, error) {
	b = trailingComma.ReplaceAll(b, []byte("$1"))
	var one map[string][]string
	if err := json.Unmarshal(b, &one); err == nil {
		return one, nil
	}
	var many []map[string][]string
	if err := json.Unmarshal(b, &many); err != nil || len(many) == 0 {
		return nil, fmt.Errorf("摄像头配置解析不了：%v", err)
	}
	return many[0], nil
}

func fstr(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// sizeLists 带尺寸的列表：每项几个数（格式, 宽, 高, 第四个）
var sizeLists = []string{"android.scaler.availableStreamConfigurations", "android.scaler.availableMinFrameDurations", "android.scaler.availableStallDurations"}

// cameraConfig 以模拟器后摄为底，换成这个摄像头的参数。
func cameraConfig(base map[string][]string, c CameraInfo) map[string][]string {
	out := map[string][]string{}
	for k, v := range base {
		out[k] = append([]string(nil), v...)
	}
	w, h := c.Width, c.Height
	if c.Facing == "front" {
		out["android.lens.facing"] = []string{"FRONT"}
		out["android.flash.info.available"] = []string{"FALSE"}
	} else {
		out["android.lens.facing"] = []string{"BACK"}
	}
	if len(c.FocalLengths) > 0 {
		out["android.lens.info.availableFocalLengths"] = nil
		for _, f := range c.FocalLengths {
			out["android.lens.info.availableFocalLengths"] = append(out["android.lens.info.availableFocalLengths"], fstr(f))
		}
	}
	if len(c.Apertures) > 0 {
		out["android.lens.info.availableApertures"] = nil
		for _, a := range c.Apertures {
			out["android.lens.info.availableApertures"] = append(out["android.lens.info.availableApertures"], fstr(a))
		}
	}
	if c.Orientation != 0 {
		out["android.sensor.orientation"] = []string{strconv.Itoa(c.Orientation)}
	}
	if w < 640 || h < 480 || w > 12000 || h > 12000 {
		return out
	}
	ws, hs := strconv.Itoa(w), strconv.Itoa(h)
	out["android.sensor.info.pixelArraySize"] = []string{ws, hs}
	out["android.sensor.info.activeArraySize"] = []string{"0", "0", ws, hs}
	out["android.sensor.info.preCorrectionActiveArraySize"] = []string{"0", "0", ws, hs}
	phys := c.PhysicalSize
	if len(phys) != 2 || phys[0] <= 0 {
		phys = []float64{float64(w) * 0.0012, float64(h) * 0.0012} // 1.2µm 像素
	}
	out["android.sensor.info.physicalSize"] = []string{fmt.Sprintf("%.4f", phys[0]), fmt.Sprintf("%.4f", phys[1])}
	out["android.jpeg.maxSize"] = []string{strconv.Itoa(w*h*3/2 + 65536)}
	// 尺寸列表：原来的最大尺寸（模拟器传感器尺寸）换成真机的；超过真机传感器的去掉；RAW（格式 32）只能是整个传感器
	ow, _ := strconv.Atoi(base["android.sensor.info.pixelArraySize"][0])
	oh, _ := strconv.Atoi(base["android.sensor.info.pixelArraySize"][1])
	for _, key := range sizeLists {
		v := out[key]
		var nv []string
		for i := 0; i+3 < len(v); i += 4 {
			fw, _ := strconv.Atoi(v[i+1])
			fh, _ := strconv.Atoi(v[i+2])
			rec := []string{v[i], v[i+1], v[i+2], v[i+3]}
			if (fw == ow && fh == oh) || v[i] == "32" {
				rec[1], rec[2] = ws, hs
				if key == "android.scaler.availableStallDurations" && v[i] == "33" {
					rec[3] = strconv.Itoa(int(math.Round(float64(w*h) * 6.5))) // 真机约 6.5 纳秒 / 像素
				}
			} else if fw > w || fh > h {
				continue
			}
			nv = append(nv, rec...)
		}
		out[key] = nv
	}
	return out
}

// pickCamera 机型里这个朝向的第一个摄像头；没有用一般值。
func pickCamera(m *Model, facing string) CameraInfo {
	if m != nil {
		for _, c := range m.Cameras {
			if c.Facing == facing && c.Width > 0 {
				if c.Orientation == 0 {
					c.Orientation = defaultCameras[facing].Orientation
				}
				return c
			}
		}
	}
	return defaultCameras[facing]
}

// cameraBinds 镜像带模拟摄像头时，开机前挂的前后摄配置。
func (s *Service) cameraBinds(ctx context.Context, id, image string, m *Model) []string {
	if s.imageLabel(ctx, image, "prism.camera") != "1" {
		return nil
	}
	const back = "/vendor/etc/config/emu_camera_back.json"
	mods, err := s.hwModules(ctx, image, map[string]string{"etc/emu_camera_back.json": back})
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(mods["etc/emu_camera_back.json"])
	if err != nil {
		return nil
	}
	base, err := parseCameraJSON(raw)
	if err != nil || len(base["android.sensor.info.pixelArraySize"]) != 2 {
		log.Printf("手机 %s：模拟摄像头的配置认不出，摄像头参数照镜像：%v", id, err)
		return nil
	}
	dir := filepath.Join(s.phoneDir(id), "camera")
	_ = os.MkdirAll(dir, 0o755)
	var binds []string
	for _, facing := range []string{"back", "front"} {
		b, _ := json.MarshalIndent(cameraConfig(base, pickCamera(m, facing)), "", " ")
		p := filepath.Join(dir, "emu_camera_"+facing+".json")
		if os.WriteFile(p, b, 0o644) != nil {
			return nil
		}
		binds = append(binds, p+":/vendor/etc/config/emu_camera_"+facing+".json:ro")
	}
	return binds
}
