package phones

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// 系统特性（pm list features）照真机（2026-11-24，学魔云腾：它的机型包录了真机的特性列表，云机照着报）。
// 以前所有手机报的都是镜像自带的 64 项：有真机没有的 android.hardware.ethernet，没有真机都有的蓝牙 LE、GPS、陀螺仪、
// 光线、距离、NFC、指纹……App 用 hasSystemFeature 一查就和机型对不上。
// 做法：开机前把镜像的 /vendor/etc/permissions/handheld_core_hardware.xml 换成「原文件 + 真机多出来的特性 +
// 真机没有的特性标成 unavailable-feature」（安卓读完每个特性文件都会把 unavailable 的删掉，所以写在哪个文件里都生效）。
// 换的是镜像里本来就有的文件名，不会让 /vendor/etc/permissions 变成没标签的目录。
// 不是全照搬，有几类按板子来（照搬会让 App 用上板子没有的能力，或者系统服务起不来）：
//   - 显卡（Vulkan / OpenGL 版本）、密钥库（keystore 版本、StrongBox）、内存档位：按板子真实能力
//   - 需要对应硬件服务、声称了却没有会让系统服务崩的（红外、WiFi Aware / RTT / 直连、UWB、Thread……）：不加
//   - 传感器：只报这台手机真有的（sensorshim.go：真机有的那几种里我们能喂数据的）
//   - 电话、WiFi、蓝牙、触屏、屏幕方向、定位、麦克风这些在用的：不删（电话的子特性是安卓 14 起框架要的，真机包是 13 的也不删）
//   - 软件特性（android.software.*）按镜像的系统版本来，只补几个纯标志（verified_boot、midi……）
//   - 厂商特性（com.vivo.*、com.mediatek.* ……）照搬；谷歌的（com.google.*、cn.google.*）按镜像（cn.google.services 会让谷歌服务按国内版跑）

var (
	featKeepBoard = []string{"android.hardware.vulkan.", "android.hardware.opengles.", "android.hardware.hardware_keystore",
		"android.hardware.keystore.", "android.hardware.strongbox_keystore", "android.hardware.identity_credential",
		"android.hardware.security.model.", "android.hardware.ram.", "android.hardware.device_unique_attestation"}
	featNeverAdd = []string{"android.hardware.consumerir", "android.hardware.wifi.aware", "android.hardware.wifi.rtt",
		"android.hardware.wifi.direct", "android.hardware.uwb", "android.hardware.thread_network", "android.hardware.type.",
		"android.hardware.ethernet", "android.hardware.context_hub", "android.hardware.sensor."}
	featNeverRemove = []string{"android.hardware.telephony", "android.hardware.wifi", "android.hardware.bluetooth",
		"android.hardware.touchscreen", "android.hardware.faketouch", "android.hardware.screen.", "android.hardware.location",
		"android.hardware.microphone", "android.hardware.audio.output", "android.hardware.sensor.", "android.hardware.usb.accessory"}
	featSafeSoftware = map[string]bool{"android.software.midi": true, "android.software.verified_boot": true,
		"android.software.file_based_encryption": true, "android.software.securely_removes_users": true,
		"android.software.opengles.deqp.level": true, "android.software.vulkan.deqp.level": true, "android.software.erofs": true,
		"android.software.connectionservice": true}
)

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// parseFeatureList 机型档案里的列表（「名字」或「名字=版本」）。
func parseFeatureList(list []string) map[string]int {
	out := map[string]int{}
	for _, f := range list {
		name, ver, _ := strings.Cut(strings.TrimSpace(f), "=")
		if name == "" {
			continue
		}
		v, _ := strconv.Atoi(ver)
		out[name] = v
	}
	return out
}

// planFeatures 比对镜像和真机的特性：要加的（名字 → 版本）、要标成没有的。
func planFeatures(img, real map[string]int, haveSensors map[string]bool) (add map[string]int, remove []string) {
	add = map[string]int{}
	for name, ver := range real {
		if _, ok := img[name]; ok {
			continue
		}
		switch {
		case strings.HasPrefix(name, "android.hardware."):
			if hasPrefixAny(name, featKeepBoard) || (hasPrefixAny(name, featNeverAdd) && !haveSensors[name]) {
				continue
			}
		case strings.HasPrefix(name, "android.software."):
			if !featSafeSoftware[name] {
				continue
			}
		case strings.HasPrefix(name, "android."), strings.HasPrefix(name, "com.google."), strings.HasPrefix(name, "cn.google."):
			continue
		}
		add[name] = ver
	}
	for name := range img {
		if _, ok := real[name]; ok || !strings.HasPrefix(name, "android.hardware.") {
			continue
		}
		if hasPrefixAny(name, featKeepBoard) || hasPrefixAny(name, featNeverRemove) {
			continue
		}
		remove = append(remove, name)
	}
	sort.Strings(remove)
	return add, remove
}

// parseFeatureXML 一个特性文件里声明的特性（名字 → 版本）。
func parseFeatureXML(b []byte, into map[string]int) {
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := d.Token()
		if err != nil {
			return
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "feature" {
			continue
		}
		var name string
		var ver int
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "name":
				name = a.Value
			case "version":
				ver, _ = strconv.Atoi(a.Value)
			}
		}
		if name != "" {
			into[name] = ver
		}
	}
}

// featuresXML 原文件 + 要加的 + 标成没有的，插在最后的 </permissions> 前面。
func featuresXML(orig []byte, add map[string]int, remove []string) ([]byte, error) {
	i := bytes.LastIndex(orig, []byte("</permissions>"))
	if i < 0 {
		return nil, fmt.Errorf("特性文件里没有 </permissions>")
	}
	names := make([]string, 0, len(add))
	for n := range add {
		names = append(names, n)
	}
	sort.Strings(names)
	var b bytes.Buffer
	b.Write(orig[:i])
	for _, n := range names {
		if add[n] != 0 {
			fmt.Fprintf(&b, "    <feature name=\"%s\" version=\"%d\" />\n", xmlEscape(n), add[n])
		} else {
			fmt.Fprintf(&b, "    <feature name=\"%s\" />\n", xmlEscape(n))
		}
	}
	for _, n := range remove {
		fmt.Fprintf(&b, "    <unavailable-feature name=\"%s\" />\n", xmlEscape(n))
	}
	b.Write(orig[i:])
	return b.Bytes(), nil
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

const featureFile = "/vendor/etc/permissions/handheld_core_hardware.xml"

// imageFeatures 镜像 /vendor/etc/permissions 里声明的特性和 handheld_core_hardware.xml 原文（缓存在数据目录）。
func (s *Service) imageFeatures(ctx context.Context, image string) (map[string]int, []byte, error) {
	hwModulesMu.Lock()
	defer hwModulesMu.Unlock()
	dir, err := s.imageCacheDir(ctx, image)
	if err != nil {
		return nil, nil, err
	}
	perm := filepath.Join(dir, "vendor-permissions")
	if !fileExists(filepath.Join(perm, ".done")) {
		cid, err := s.run.Run(ctx, "docker", "create", image, "/system/bin/sh")
		if err != nil {
			return nil, nil, err
		}
		cid = strings.TrimSpace(cid)
		defer s.run.Run(ctx, "docker", "rm", cid) //nolint:errcheck
		_ = os.RemoveAll(perm)
		_ = os.MkdirAll(dir, 0o755)
		if out, err := s.run.Run(ctx, "docker", "cp", cid+":/vendor/etc/permissions", perm); err != nil {
			_ = os.RemoveAll(perm)
			return nil, nil, fmt.Errorf("取镜像特性文件：%v %s", err, out)
		}
		_ = os.WriteFile(filepath.Join(perm, ".done"), nil, 0o644)
	}
	orig, err := os.ReadFile(filepath.Join(perm, filepath.Base(featureFile)))
	if err != nil {
		return nil, nil, err
	}
	feats := map[string]int{}
	files, _ := filepath.Glob(filepath.Join(perm, "*.xml"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			parseFeatureXML(b, feats)
		}
	}
	return feats, orig, nil
}

// featureBinds 机型录了真机特性列表时，开机前挂进去的特性文件。
func (s *Service) featureBinds(ctx context.Context, id, image string, m *Model) []string {
	if m == nil || len(m.Features) == 0 {
		return nil
	}
	img, orig, err := s.imageFeatures(ctx, image)
	if err != nil {
		log.Printf("手机 %s：读镜像的系统特性失败，特性照镜像：%v", id, err)
		return nil
	}
	add, remove := planFeatures(img, parseFeatureList(m.Features), sensorFeatures(sensorMask(m)))
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	out, err := featuresXML(orig, add, remove)
	if err != nil {
		log.Printf("手机 %s：%v", id, err)
		return nil
	}
	dir := filepath.Join(s.phoneDir(id), "features")
	_ = os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, filepath.Base(featureFile))
	if err := os.WriteFile(p, out, 0o644); err != nil {
		return nil
	}
	return []string{p + ":" + featureFile + ":ro"}
}
