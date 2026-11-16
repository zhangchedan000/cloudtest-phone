package phones

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
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
}

type Model struct {
	ID              string         `json:"id"`
	Schema          int            `json:"schema"`
	Verified        bool           `json:"verified"`
	GPUFamily       string         `json:"gpuFamily"`
	Market          string         `json:"market"`
	Values          map[string]any `json:"values"`
	AndroidVersions []int          `json:"androidVersions"`
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
	return &cat, models, nil
}
