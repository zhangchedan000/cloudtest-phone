package phones

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/zhangchedan000/prism-phone/agent/internal/fakefs"
)

// 字段清单和机型库都来自仓库根目录的 profiles/（编译时打包进程序），和管理页读的是同一份。

type Sink struct {
	Kind  string   `json:"kind"`
	Names []string `json:"names,omitempty"`
}

type Field struct {
	Key      string   `json:"key"`
	Group    string   `json:"group"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Modes    []string `json:"modes"`
	Apply    string   `json:"apply"`
	Readonly bool     `json:"readonly,omitempty"`
	Stage    string   `json:"stage,omitempty"`
	Sink     Sink     `json:"sink"`
}

type Catalog struct {
	Version int     `json:"version"`
	Fields  []Field `json:"fields"`
	Raw     json.RawMessage
	// CPUTrees 真机 CPU 目录模板（profiles/cpu/<名>.json），机型档案的 cpuTree 按名字引用（踩坑 #147）
	CPUTrees map[string]*fakefs.CPUTree `json:"-"`
}

type Model struct {
	ID              string         `json:"id"`
	Schema          int            `json:"schema"`
	Verified        bool           `json:"verified"`
	GPUFamily       string         `json:"gpuFamily"`
	Market          string         `json:"market"`
	Values          map[string]any `json:"values"`
	AndroidVersions []int          `json:"androidVersions"`
	// CPUTree 用哪份真机 CPU 目录模板（profiles/cpu/<名>.json）。有就整个 /sys/devices/system/cpu 换成它，
	// cpuParts / cpuMaxFreq 不用（踩坑 #147）。没有的机型 CPU 目录还是板子的，面板提示。
	CPUTree string `json:"cpuTree,omitempty"`
	// CPUMismatch 这款机型的 CPU 和板子不是同一代（例如天玑 7200 的 ARMv9 A715 / A510），指令集伪装不了，面板提示
	CPUMismatch string `json:"cpuMismatch,omitempty"`
	// ImeiTacs 这款机型真实的 IMEI 前 8 位（型号核准码 TAC），每台手机固定挑一个。没有就随机 35xxxxxx——
	// 对不上任何真机型，查 IMEI 归属就露馅（2026-11-23；数据取自魔云腾的 TAC 库，按型号代码精确匹配）。
	ImeiTacs []string `json:"imeiTacs"`
	// Fingerprints 按安卓版本（"14"/"15"）存的真实 stock 指纹（从设备树/固件抠）。
	// 必须和手机实际跑的安卓版本一致，否则指纹里的 release 和 ro.build.version.release 对不上。
	Fingerprints map[string]string `json:"fingerprints"`
	// BuildProps 按安卓版本存的真机各分区身份属性（从真实出厂 build.prop 抠，host/extract-buildprops.py，踩坑 #52）。
	// 有就照搬真机的分区布局（system / product 分区是通用系统镜像的值，odm 指纹停在出厂版本……），
	// 不再把 9 个分区统一改成同一个机型。
	BuildProps map[string]map[string]string `json:"buildProps"`
	Raw        json.RawMessage
}

func (c *Catalog) Field(key string) (Field, bool) {
	for _, f := range c.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// LoadProfiles 从打包的 profiles 目录读取字段清单和全部机型。
func LoadProfiles(fsys fs.FS, dir string) (*Catalog, []Model, error) {
	raw, err := fs.ReadFile(fsys, path.Join(dir, "fields.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("读字段清单：%w", err)
	}
	var cat Catalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, nil, fmt.Errorf("字段清单格式错误：%w", err)
	}
	cat.Raw = raw
	entries, err := fs.ReadDir(fsys, path.Join(dir, "models"))
	if err != nil {
		return nil, nil, fmt.Errorf("读机型库：%w", err)
	}
	var models []Model
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, "models", e.Name()))
		if err != nil {
			return nil, nil, err
		}
		var m Model
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, nil, fmt.Errorf("机型 %s 格式错误：%w", e.Name(), err)
		}
		m.Raw = data
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	cat.CPUTrees = map[string]*fakefs.CPUTree{}
	if entries, err := fs.ReadDir(fsys, path.Join(dir, "cpu")); err == nil {
		for _, e := range entries {
			if e.IsDir() || path.Ext(e.Name()) != ".json" {
				continue
			}
			data, err := fs.ReadFile(fsys, path.Join(dir, "cpu", e.Name()))
			if err != nil {
				return nil, nil, err
			}
			var t fakefs.CPUTree
			if err := json.Unmarshal(data, &t); err != nil {
				return nil, nil, fmt.Errorf("CPU 模板 %s 格式错误：%w", e.Name(), err)
			}
			if t.Name == "" {
				t.Name = e.Name()[:len(e.Name())-len(".json")]
			}
			if err := t.Validate(); err != nil {
				return nil, nil, err
			}
			cat.CPUTrees[t.Name] = &t
		}
	}
	for _, m := range models {
		if m.CPUTree != "" && cat.CPUTrees[m.CPUTree] == nil {
			return nil, nil, fmt.Errorf("机型 %s 引用的 CPU 模板 %s 不存在", m.ID, m.CPUTree)
		}
	}
	return &cat, models, nil
}
