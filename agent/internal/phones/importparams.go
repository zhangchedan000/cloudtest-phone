package phones

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 导入真机身份（真机参数）：把一份真机的 getprop dump（或 key=value 文本）里的身份值，
// 套到某台云手机上（品牌/型号/设备代号/指纹/序列号/MAC/芯片等，全部按「手动」模式覆盖）。
// 用途：买来的「真机参数」或自己扒的真机 dump，一键套进一台手机，不用手动逐项填。
//
// 只导入「身份」字段，不碰硬件能力（CPU/GPU/屏幕跟机型走）、也不碰跟随代理的时区语言。
// 建议：导入前先给这台手机选一个 SoC/显卡接近的机型（都 Mali-G610），否则 CPU/显卡会和导入的型号对不上。

var getpropLine = regexp.MustCompile(`^\[([^\]]+)\]:\s*\[(.*)\]\s*$`)

// parseGetpropDump 解析 getprop 输出（[key]: [value]）或 key=value / key: value 文本。
func parseGetpropDump(dump string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := getpropLine.FindStringSubmatch(line); m != nil {
			out[strings.TrimSpace(m[1])] = strings.TrimSpace(m[2])
			continue
		}
		// 退回 key=value 或 key: value（只收 ro./persist. 开头的，避免杂线）
		for _, sep := range []string{"=", ": "} {
			if k, v, ok := strings.Cut(line, sep); ok {
				k = strings.TrimSpace(k)
				if strings.HasPrefix(k, "ro.") || strings.HasPrefix(k, "persist.") {
					out[k] = strings.TrimSpace(v)
				}
				break
			}
		}
	}
	return out
}

// 有些身份值在 getprop 里的 key 和字段自身的 sink 不同（字段写到 setting/netif，但真机 dump 里是 ro.* 属性）。
var paramSourceProp = map[string]string{
	"wifiMac":      "ro.device.wifi_mac",     // 字段 sink 是 netif，但真机 dump 里是这个属性
	"bluetoothMac": "ro.boot.btmacaddr",      // 字段 sink 是 setting，真机 dump 里是这个属性
	"androidId":    "ro.settings.android_id", // 少数 dump 带这个（一般 getprop 没有，有就用）
}

// deviceParamsToFields 把解析出来的属性映射成「手动」字段设置。
// 返回：字段设置、成功导入的字段 key、以及在 dump 里没找到对应值的身份字段 key。
func (s *Service) deviceParamsToFields(props map[string]string) (fields map[string]store.FieldSetting, imported, missing []string) {
	fields = map[string]store.FieldSetting{}
	for _, f := range s.cat.Fields {
		if !contains(f.Modes, "manual") {
			continue // 只能导入支持手动的字段
		}
		// 时区/语言跟随代理，不随身份导入覆盖。
		if f.Key == "timezone" || f.Key == "locale" {
			continue
		}
		// 这台机器才有的能力字段（CPU/GPU/屏幕/传感器/电池等）不从 getprop 导入。
		switch f.Sink.Kind {
		case "fakefile", "boot", "uinput", "quota", "battery", "sensor-hal", "ril", "virt-wifi", "location", "board", "image":
			continue
		}
		var key string
		if sp, ok := paramSourceProp[f.Key]; ok {
			key = sp
		} else if f.Sink.Kind == "prop" && len(f.Sink.Names) > 0 {
			key = f.Sink.Names[0]
		} else {
			continue
		}
		v, ok := props[key]
		if !ok || strings.TrimSpace(v) == "" {
			missing = append(missing, f.Key)
			continue
		}
		fields[f.Key] = store.FieldSetting{Mode: "manual", Value: v}
		imported = append(imported, f.Key)
	}
	return fields, imported, missing
}

// ImportDeviceParams 解析真机 getprop dump 并套到指定手机上（按手动模式）。
// 返回导入了哪些字段、哪些身份字段在 dump 里没找到。下次重启生效。
func (s *Service) ImportDeviceParams(id, dump string) (imported, missing []string, err error) {
	props := parseGetpropDump(dump)
	if len(props) == 0 {
		return nil, nil, fmt.Errorf("没解析出任何属性：请粘贴真机的 getprop 输出（[key]: [value]）或 key=value 文本")
	}
	fields, imp, miss := s.deviceParamsToFields(props)
	if len(fields) == 0 {
		return nil, nil, fmt.Errorf("dump 里没有可识别的身份属性（如 ro.product.brand / ro.build.fingerprint）")
	}
	// buildId / incremental 从指纹里自动派生，导入了指纹就不算缺失。
	if _, ok := fields["fingerprint"]; ok {
		var m2 []string
		for _, k := range miss {
			if k != "buildId" && k != "incremental" {
				m2 = append(m2, k)
			}
		}
		miss = m2
	}
	if err := s.Save(id, SaveRequest{Fields: fields}); err != nil {
		return nil, nil, err
	}
	return imp, miss, nil
}
