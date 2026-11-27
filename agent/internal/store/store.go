// Package store 保存管家程序的数据：云手机、代理库、分组库。
//
// 规矩（同 Prism）：代理、分组各存一份，云手机只存引用（ProxyID、GroupID、ModelID）和改过的字段。
// 数据写在一个 JSON 文件里，先写临时文件再改名，断电也不会写坏。
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FieldSetting 一个参数在某台云手机上的设置：用什么模式、手动/随机出来的值。
type FieldSetting struct {
	Mode   string `json:"mode"`
	Value  any    `json:"value,omitempty"`
	Locked bool   `json:"locked,omitempty"`
}

type Phone struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	HostID         string `json:"hostId"`
	Status         string `json:"status"` // running / stopped / starting / error（由运行状态覆盖）
	AndroidVersion int    `json:"androidVersion"`
	ModelID        string `json:"modelId"`
	ProxyID        string `json:"proxyId"` // 当前在用的代理
	// ProxyIDs 这台挂的所有代理（可以随时切换，2026-11-15 用户要求「一台手机可以用多个 IP」）；空 = 只有 ProxyID 一个。
	ProxyIDs     []string                `json:"proxyIds,omitempty"`
	GroupID      string                  `json:"groupId"`
	Note         string                  `json:"note,omitempty"`
	CreatedAt    time.Time               `json:"createdAt"`
	LastActiveAt *time.Time              `json:"lastActiveAt,omitempty"`
	Fields       map[string]FieldSetting `json:"fields"`
	// IP 在容器网段里的固定地址（代理分流按它来），创建时分配，永不改变。
	IP string `json:"ip"`
	// Gapps 带谷歌服务（GMS / Play 商店）：开机用谷歌服务镜像而不是干净 AOSP 镜像。
	// 需要这台机器装了对应安卓版本的谷歌服务镜像（见 host/build-gapps-image.sh）。
	Gapps bool `json:"gapps,omitempty"`
	// Adb「开发者选项 → USB 调试」开着吗。默认关（真手机普通用户都是关的，App 能读到这个开关）；
	// 要用 ADB 时在管理页打开（踩坑 #56）。
	Adb bool `json:"adb,omitempty"`
	// Location 虚拟定位（经纬度）；nil = 没设（手机本来就没有真 GPS）。2026-11-27 用户要求，对齐魔云腾。
	Location *LatLng `json:"location,omitempty"`
}

// LatLng 一个经纬度点。
type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type ProxyCheck struct {
	OK        bool      `json:"ok"`
	ExitIP    string    `json:"exitIp,omitempty"`
	Country   string    `json:"country,omitempty"`
	Region    string    `json:"region,omitempty"`
	City      string    `json:"city,omitempty"`
	Timezone  string    `json:"timezone,omitempty"`
	LatencyMs int       `json:"latencyMs,omitempty"`
	UDP       *bool     `json:"udp,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
	Error     string    `json:"error,omitempty"`
}

type Proxy struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Protocol string      `json:"protocol"`
	Address  string      `json:"address"`
	Note     string      `json:"note,omitempty"`
	Check    *ProxyCheck `json:"check,omitempty"`
}

type Group struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type State struct {
	Schema  int     `json:"schema"`
	Phones  []Phone `json:"phones"`
	Proxies []Proxy `json:"proxies"`
	Groups  []Group `json:"groups"`
	// Servers 别的云手机服务器（多台机器统一管理，2026-11-15）：管理页直接连它们，口令存在这里。
	Servers []Server `json:"servers,omitempty"`
}

// Server 另一台跑着管家程序的机器。
type Server struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`   // 例如 https://phone2.example.com（管理页的地址）
	Token string `json:"token"` // 那台的 PP_TOKEN
}

const currentSchema = 1

var ErrNotFound = errors.New("不存在")

type Store struct {
	mu    sync.RWMutex
	path  string
	state State
}

// Open 读取数据文件；文件不存在就从空数据开始。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "state.json"), state: State{Schema: currentSchema}}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.state); err != nil {
		return nil, fmt.Errorf("数据文件 %s 读不了：%w", s.path, err)
	}
	if s.state.Schema > currentSchema {
		return nil, fmt.Errorf("数据文件是新版本（schema %d），请升级管家程序", s.state.Schema)
	}
	return s, nil
}

// Snapshot 返回当前数据的副本，调用方随便改不影响存储。
func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, _ := json.Marshal(s.state)
	var out State
	_ = json.Unmarshal(raw, &out)
	return out
}

// Update 在锁里修改数据并保存；fn 返回错误就不保存。
func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyLocked()
	if err := fn(&next); err != nil {
		return err
	}
	if err := s.writeLocked(next); err != nil {
		return err
	}
	s.state = next
	return nil
}

func (s *Store) copyLocked() State {
	raw, _ := json.Marshal(s.state)
	var out State
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s *Store) writeLocked(st State) error {
	st.Schema = currentSchema
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	// 先写临时文件、刷到盘上再改名，改名后把目录也刷一下：突然断电时 state.json 要么是旧的、要么是新的，
	// 不会变成空文件（eMMC / NVMe 上只 rename 不 fsync，断电后可能只剩个 0 字节文件，手机列表全丢）。
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func (st *State) Phone(id string) (*Phone, error) {
	for i := range st.Phones {
		if st.Phones[i].ID == id {
			return &st.Phones[i], nil
		}
	}
	return nil, fmt.Errorf("云手机 %s %w", id, ErrNotFound)
}

func (st *State) Proxy(id string) *Proxy {
	for i := range st.Proxies {
		if st.Proxies[i].ID == id {
			return &st.Proxies[i]
		}
	}
	return nil
}

// FreeID 返回一个还没被代理、分组、设备用过的 ID：base 没人用就用 base，否则 base-2、base-3……
// 必须在 Update 里调用（拿着锁），否则两个请求可能拿到同一个。
// 以前 ID 只用时间戳，同一秒（或同一毫秒）建两次会重复，mihomo 里就有两个同名出口（2026-11-15 实机测出）。
func (st *State) FreeID(base string) string {
	used := make(map[string]bool, len(st.Proxies)+len(st.Groups)+len(st.Phones))
	for _, p := range st.Proxies {
		used[p.ID] = true
	}
	for _, g := range st.Groups {
		used[g.ID] = true
	}
	for _, p := range st.Phones {
		used[p.ID] = true
	}
	for _, sv := range st.Servers {
		used[sv.ID] = true
	}
	id := base
	for n := 2; used[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}
