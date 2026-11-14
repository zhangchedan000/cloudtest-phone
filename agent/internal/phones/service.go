// Package phones 是管家程序的核心：创建、开机、关机、删除云手机，把参数落到安卓里。
package phones

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/docker"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// Docker 是用到的 Docker 接口（测试时换成假的）。
type Docker interface {
	CreateContainer(ctx context.Context, spec docker.ContainerSpec) (string, error)
	StartContainer(ctx context.Context, name string) error
	StopContainer(ctx context.Context, name string, timeoutSec int) error
	RemoveContainer(ctx context.Context, name string) error
	InspectContainer(ctx context.Context, name string) (*docker.ContainerInfo, error)
	EnsureNetwork(ctx context.Context, name, subnet, gateway string) error
}

// Config 这台机器（板子）的配置。板子相关的东西都在这里，换板子只改配置。
type Config struct {
	HostID     string
	HostName   string
	GPUFamily  string         // "software" 表示没有显卡（开发机）
	GPUMode    string         // Redroid 的 gpu_mode：guest（软件渲染）/ host
	Images     map[int]string // 安卓版本 → 镜像
	DataDir    string         // 每台云手机的 /data 放在 DataDir/phones/<id>/data
	Network    string         // 容器网络名
	Subnet     string         // 例如 10.88.0.0/16
	MemoryMB   int64          // 每台容器内存上限（保护宿主机）
	CPUs       float64        // 每台容器 CPU 上限
	MaxRunning int            // 同时最多运行几台
	// DevHost 开发机：允许「直连（仅开发）」代理，方便没代理时测试。正式机器必须是 false。
	DevHost bool
	// HostCPUs 宿主机核数（0 = 自动）；BootTimeout / PollEvery 等开机的超时和间隔（0 = 默认）。
	HostCPUs    int
	BootTimeout time.Duration
	PollEvery   time.Duration
}

const DirectDevProxyID = "direct-dev"

type Service struct {
	cfg      Config
	docker   Docker
	store    *store.Store
	cat      *Catalog
	models   []Model
	now      func() time.Time
	run      Runner
	raw      RawRunner
	boots    bootTracker
	settings settingsTracker
	fakes    fakeTracker
	// async 为 false 时伪造文件同步挂载（测试用）。
	async bool
}

func NewService(cfg Config, d Docker, st *store.Store, cat *Catalog, models []Model) *Service {
	return &Service{cfg: cfg, docker: d, store: st, cat: cat, models: models, now: time.Now, run: execRunner{}, raw: execRaw{}, async: true}
}

func (s *Service) Models() []Model   { return s.models }
func (s *Service) Catalog() *Catalog { return s.cat }
func (s *Service) Config() Config    { return s.cfg }

func (s *Service) model(id string) *Model {
	for i := range s.models {
		if s.models[i].ID == id {
			return &s.models[i]
		}
	}
	return nil
}

func containerName(id string) string { return "pp-" + id }

// ---------- 创建 ----------

type CreateRequest struct {
	Name           string `json:"name"`
	ModelID        string `json:"modelId"`
	AndroidVersion int    `json:"androidVersion"`
	ProxyID        string `json:"proxyId"`
	GroupID        string `json:"groupId"`
	Note           string `json:"note"`
}

var ErrInvalid = errors.New("参数不对")

func (s *Service) Create(req CreateRequest) (store.Phone, error) {
	m := s.model(req.ModelID)
	if m == nil {
		return store.Phone{}, fmt.Errorf("%w：机型 %q 不存在", ErrInvalid, req.ModelID)
	}
	if _, ok := s.cfg.Images[req.AndroidVersion]; !ok {
		return store.Phone{}, fmt.Errorf("%w：这台机器没有 Android %d 的镜像", ErrInvalid, req.AndroidVersion)
	}
	if strings.TrimSpace(req.Name) == "" {
		return store.Phone{}, fmt.Errorf("%w：名称不能为空", ErrInvalid)
	}
	var created store.Phone
	err := s.store.Update(func(st *store.State) error {
		if req.ProxyID != "" && req.ProxyID != DirectDevProxyID && st.Proxy(req.ProxyID) == nil {
			return fmt.Errorf("%w：代理 %q 不存在", ErrInvalid, req.ProxyID)
		}
		ip, err := s.nextIP(st)
		if err != nil {
			return err
		}
		fields := map[string]store.FieldSetting{}
		for _, f := range s.cat.Fields {
			mode := f.Modes[0]
			for _, candidate := range f.Modes {
				if candidate == "proxy" {
					mode = "proxy" // 时区、语言、位置默认跟随代理
				}
			}
			fs := store.FieldSetting{Mode: mode}
			if mode == "random" {
				fs.Value = RandomValue(f.Key, m)
			}
			fields[f.Key] = fs
		}
		created = store.Phone{
			ID:             fmt.Sprintf("%x", s.now().UnixNano())[4:],
			Name:           strings.TrimSpace(req.Name),
			HostID:         s.cfg.HostID,
			Status:         "stopped",
			AndroidVersion: req.AndroidVersion,
			ModelID:        req.ModelID,
			ProxyID:        req.ProxyID,
			GroupID:        req.GroupID,
			Note:           req.Note,
			CreatedAt:      s.now().UTC(),
			Fields:         fields,
			IP:             ip,
		}
		st.Phones = append(st.Phones, created)
		return nil
	})
	return created, err
}

// nextIP 在容器网段里给新设备分配一个没用过的固定 IP（.1.2 起，网关是 .0.1）。
func (s *Service) nextIP(st *store.State) (string, error) {
	prefix, err := netip.ParsePrefix(s.cfg.Subnet)
	if err != nil {
		return "", fmt.Errorf("网段配置错误：%w", err)
	}
	used := map[string]bool{}
	for _, p := range st.Phones {
		used[p.IP] = true
	}
	start := prefix.Masked().Addr().As4()
	start[2], start[3] = 1, 2
	for addr := netip.AddrFrom4(start); prefix.Contains(addr); addr = addr.Next() {
		if b := addr.As4(); b[3] == 0 || b[3] == 255 {
			continue
		}
		if !used[addr.String()] {
			return addr.String(), nil
		}
	}
	return "", errors.New("网段里的 IP 用完了")
}

func (s *Service) gateway() string {
	prefix, _ := netip.ParsePrefix(s.cfg.Subnet)
	b := prefix.Masked().Addr().As4()
	b[3] = 1
	return netip.AddrFrom4(b).String()
}

// ---------- 参数落地 ----------

// ResolvedField 一个字段最终生效的值，以及有没有真的落到安卓里。
type ResolvedField struct {
	Key     string `json:"key"`
	Value   any    `json:"value"`
	Applied bool   `json:"applied"`
	Reason  string `json:"reason,omitempty"`
}

func (s *Service) resolve(p store.Phone, st store.State) []ResolvedField {
	m := s.model(p.ModelID)
	var proxyCheck *store.ProxyCheck
	if px := st.Proxy(p.ProxyID); px != nil {
		proxyCheck = px.Check
	}
	var out []ResolvedField
	for _, f := range s.cat.Fields {
		setting, ok := p.Fields[f.Key]
		if !ok {
			setting = store.FieldSetting{Mode: f.Modes[0]}
		}
		var value any
		switch setting.Mode {
		case "model":
			if m != nil {
				value = m.Values[f.Key]
			}
		case "proxy":
			if proxyCheck != nil && proxyCheck.OK {
				switch f.Key {
				case "timezone":
					value = proxyCheck.Timezone
				case "locale":
					value = localeFor(proxyCheck.Country)
				case "simCountry":
					value = proxyCheck.Country
				}
			}
		case "image":
			if f.Key == "androidVersion" {
				value = p.AndroidVersion
			}
		default:
			value = setting.Value
		}
		rf := ResolvedField{Key: f.Key, Value: value}
		switch {
		case value == nil:
			rf.Reason = "没有值"
		case f.Stage == "later":
			rf.Reason = "后期功能"
		case f.Sink.Kind == "boot":
			rf.Applied = true
		case f.Sink.Kind == "fakefile":
			rf.Applied = true
			if contains(f.Sink.Names, "uname()") {
				rf.Reason = "文件已伪造；uname() 系统调用还没拦截（待做 seccomp）"
			}
		case f.Sink.Kind == "prop" && isLiveProp(f.Key):
			// 实测：persist.* 属性用启动参数设不进去，开机完成后在容器里设置（settings.go）。
			rf.Applied = true
			rf.Reason = "开机后设置"
		case f.Sink.Kind == "prop" && !allReadOnlyProps(f.Sink.Names):
			rf.Reason = "开机后设置（这个字段还没实现）"
		case f.Sink.Kind == "prop" && strings.ContainsAny(fmt.Sprint(value), " \t"):
			// 实测：Redroid 启动参数里的值遇到空格会被截断（「POCO X4 GT」变成「POCO」），截断比不改更糟。
			rf.Reason = "值里有空格，启动参数传不了（待改用 build.prop 写入）"
		case f.Sink.Kind == "prop":
			rf.Applied = true
		default:
			rf.Reason = "落地方式（" + f.Sink.Kind + "）还没实现"
		}
		out = append(out, rf)
	}
	return out
}

func allReadOnlyProps(names []string) bool {
	for _, n := range names {
		if !strings.HasPrefix(n, "ro.") {
			return false
		}
	}
	return true
}

var countryLocale = map[string]string{
	"US": "en-US", "GB": "en-GB", "CA": "en-CA", "AU": "en-AU", "SG": "en-SG", "MY": "ms-MY", "PH": "en-PH",
	"TH": "th-TH", "VN": "vi-VN", "ID": "id-ID", "JP": "ja-JP", "KR": "ko-KR", "DE": "de-DE", "FR": "fr-FR",
	"ES": "es-ES", "IT": "it-IT", "MX": "es-MX", "BR": "pt-BR", "HK": "zh-HK", "TW": "zh-TW", "CN": "zh-CN",
}

func localeFor(country string) string {
	if l, ok := countryLocale[strings.ToUpper(country)]; ok {
		return l
	}
	if country == "" {
		return ""
	}
	return "en-US"
}

// BootArgs 把设备参数变成 Redroid 的启动参数（androidboot.* 和 ro.* 属性）。
// 每次开机都重新生成，所以「重启生效」的字段改完重启就生效。
func (s *Service) BootArgs(p store.Phone, st store.State) []string {
	args := []string{
		"androidboot.redroid_gpu_mode=" + s.cfg.GPUMode,
		"androidboot.use_memfd=true",
	}
	resolved := s.resolve(p, st)
	byKey := map[string]any{}
	for _, r := range resolved {
		byKey[r.Key] = r.Value
	}
	if size, ok := byKey["resolution"].(map[string]any); ok {
		args = append(args, fmt.Sprintf("androidboot.redroid_width=%v", size["w"]), fmt.Sprintf("androidboot.redroid_height=%v", size["h"]))
	}
	if dpi, ok := byKey["dpi"]; ok && dpi != nil {
		args = append(args, fmt.Sprintf("androidboot.redroid_dpi=%v", dpi))
	}
	if fps, ok := byKey["refreshRate"]; ok && fps != nil {
		args = append(args, fmt.Sprintf("androidboot.redroid_fps=%v", fps))
	}
	var props []string
	for _, r := range resolved {
		f, _ := s.cat.Field(r.Key)
		if !r.Applied || f.Sink.Kind != "prop" || isLiveProp(f.Key) {
			continue
		}
		val := propValue(f, r.Value)
		if val == "" {
			continue
		}
		for _, name := range f.Sink.Names {
			props = append(props, name+"="+val)
		}
	}
	sort.Strings(props)
	return append(args, props...)
}

func propValue(f Field, v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprint(int64(val))
		}
		return fmt.Sprint(val)
	case int:
		return fmt.Sprint(val)
	case []any:
		parts := make([]string, 0, len(val))
		for _, x := range val {
			parts = append(parts, fmt.Sprint(x))
		}
		return strings.Join(parts, ",")
	}
	if f.Type == "string" && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// ---------- 开机前检查 ----------

type Issue struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Check 开机前的硬性检查（和管理页的一致性检查同样的规矩，error 级别禁止开机）。
func (s *Service) Check(p store.Phone, st store.State) []Issue {
	var issues []Issue
	if p.ProxyID == DirectDevProxyID {
		if !s.cfg.DevHost {
			issues = append(issues, Issue{"error", "direct-not-allowed", "「直连」只允许在开发机上用。"})
		} else {
			issues = append(issues, Issue{"warning", "direct-dev", "直连（仅开发）：走的是机房 IP，不要登录真实账号。"})
		}
	} else if px := st.Proxy(p.ProxyID); px == nil {
		issues = append(issues, Issue{"error", "proxy-missing", "没有分配代理，禁止开机（避免暴露机房真实 IP）。"})
	} else if px.Check != nil && !px.Check.OK {
		issues = append(issues, Issue{"error", "proxy-down", fmt.Sprintf("代理「%s」检测不通，禁止开机。", px.Name)})
	} else if s.cfg.DevHost {
		// TODO：透明代理落地前，开发机上分配了真实代理也还走不了，提醒一下。
		issues = append(issues, Issue{"warning", "proxy-not-wired", "透明代理还没接上：现在开机走的仍是机房 IP。"})
	}
	m := s.model(p.ModelID)
	if m == nil {
		issues = append(issues, Issue{"error", "model-missing", "机型不存在。"})
	} else if s.cfg.GPUFamily != "software" && m.GPUFamily != s.cfg.GPUFamily {
		issues = append(issues, Issue{"error", "gpu-mismatch", fmt.Sprintf("机型显卡 %s 和机器显卡 %s 对不上。", m.GPUFamily, s.cfg.GPUFamily)})
	}
	for _, key := range []string{"androidId", "serial", "wifiMac", "bluetoothMac", "gaid"} {
		v := p.Fields[key].Value
		if v == nil || v == "" {
			continue
		}
		for _, other := range st.Phones {
			if other.ID != p.ID && other.Fields[key].Value == v {
				issues = append(issues, Issue{"error", "duplicate-" + key, fmt.Sprintf("%s 和「%s」重复。", key, other.Name)})
			}
		}
	}
	return issues
}

func blocks(issues []Issue) error {
	var msgs []string
	for _, i := range issues {
		if i.Level == "error" {
			msgs = append(msgs, i.Message)
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf("%w：%s", ErrBlocked, strings.Join(msgs, "；"))
}

var ErrBlocked = errors.New("禁止开机")

// ---------- 开机 / 关机 / 删除 ----------

func (s *Service) Start(ctx context.Context, id string) error {
	st := s.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil {
		return err
	}
	if err := blocks(s.Check(*p, st)); err != nil {
		return err
	}
	if s.cfg.MaxRunning > 0 {
		running := 0
		for _, other := range st.Phones {
			if other.ID != id && s.isRunning(ctx, other.ID) {
				running++
			}
		}
		if running >= s.cfg.MaxRunning {
			return fmt.Errorf("%w：这台机器同时最多运行 %d 台，先关掉一台", ErrBlocked, s.cfg.MaxRunning)
		}
	}
	if err := s.docker.EnsureNetwork(ctx, s.cfg.Network, s.cfg.Subnet, s.gateway()); err != nil {
		return fmt.Errorf("准备网络：%w", err)
	}
	dataDir := filepath.Join(s.cfg.DataDir, "phones", p.ID, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	binds := []string{dataDir + ":/data"}
	hasFakes, err := s.prepareFakes(*p, st)
	if err != nil {
		return fmt.Errorf("生成伪造文件：%w", err)
	}
	if hasFakes {
		binds = append(binds, s.fakeDir(p.ID)+":"+fakeMount+":ro")
	}
	// 每次开机重建容器（/data 在外面，不受影响），保证最新参数生效。
	name := containerName(p.ID)
	if err := s.docker.RemoveContainer(ctx, name); err != nil {
		return fmt.Errorf("清理旧容器：%w", err)
	}
	_, err = s.docker.CreateContainer(ctx, docker.ContainerSpec{
		Name:       name,
		Image:      s.cfg.Images[p.AndroidVersion],
		Cmd:        s.BootArgs(*p, st),
		Labels:     map[string]string{"prism-phone": "1", "prism-phone.id": p.ID},
		Binds:      binds,
		MemoryMB:   s.cfg.MemoryMB,
		NanoCPUs:   int64(s.cfg.CPUs * 1e9),
		Network:    s.cfg.Network,
		IPv4:       p.IP,
		Privileged: true,
	})
	if err != nil {
		return fmt.Errorf("创建容器：%w", err)
	}
	if err := s.docker.StartContainer(ctx, name); err != nil {
		return fmt.Errorf("启动容器：%w", err)
	}
	if s.async {
		go s.applySettings(context.Background(), p.ID)
	} else {
		s.applySettings(ctx, p.ID)
	}
	if hasFakes {
		if s.async {
			go s.applyFakes(context.Background(), p.ID)
		} else {
			s.applyFakes(ctx, p.ID)
		}
	}
	now := s.now().UTC()
	return s.store.Update(func(st *store.State) error {
		ph, err := st.Phone(id)
		if err != nil {
			return err
		}
		ph.LastActiveAt = &now
		return nil
	})
}

func (s *Service) Stop(ctx context.Context, id string) error {
	st := s.store.Snapshot()
	if _, err := st.Phone(id); err != nil {
		return err
	}
	err := s.docker.StopContainer(ctx, containerName(id), 15)
	if docker.IsNotFound(err) {
		return nil
	}
	return err
}

// Delete 删除云手机。数据不直接删，挪到 DataDir/trash 下（误删还能找回），确认没问题再手动清。
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.docker.RemoveContainer(ctx, containerName(id)); err != nil {
		return err
	}
	src := filepath.Join(s.cfg.DataDir, "phones", id)
	if _, err := os.Stat(src); err == nil {
		trash := filepath.Join(s.cfg.DataDir, "trash")
		if err := os.MkdirAll(trash, 0o755); err != nil {
			return err
		}
		if err := os.Rename(src, filepath.Join(trash, id+"-"+s.now().Format("20060102-150405"))); err != nil {
			return err
		}
	}
	return s.store.Update(func(st *store.State) error {
		for i, p := range st.Phones {
			if p.ID == id {
				st.Phones = append(st.Phones[:i], st.Phones[i+1:]...)
				return nil
			}
		}
		return store.ErrNotFound
	})
}

func (s *Service) isRunning(ctx context.Context, id string) bool {
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	return err == nil && info.State.Running
}

// PhoneView 返回给管理页的设备：存储的数据 + 运行状态 + 检查结果 + 参数落地情况。
type PhoneView struct {
	store.Phone
	Issues   []Issue         `json:"issues"`
	Resolved []ResolvedField `json:"resolved"`
	Fakes    *FakeStatus     `json:"fakes,omitempty"`
	Settings *SettingsStatus `json:"settings,omitempty"`
}

func (s *Service) List(ctx context.Context) []PhoneView {
	st := s.store.Snapshot()
	out := make([]PhoneView, 0, len(st.Phones))
	for _, p := range st.Phones {
		p.Status = s.State(ctx, p.ID)
		out = append(out, PhoneView{Phone: p, Issues: s.Check(p, st), Resolved: s.resolve(p, st), Fakes: s.fakes.get(p.ID), Settings: s.settings.get(p.ID)})
	}
	return out
}

// SaveFields 保存参数（和可选的机型、代理、分组、名称）。
type SaveRequest struct {
	Fields  map[string]store.FieldSetting `json:"fields"`
	ModelID *string                       `json:"modelId"`
	ProxyID *string                       `json:"proxyId"`
	GroupID *string                       `json:"groupId"`
	Name    *string                       `json:"name"`
	Note    *string                       `json:"note"`
}

func (s *Service) Save(id string, req SaveRequest) error {
	return s.store.Update(func(st *store.State) error {
		p, err := st.Phone(id)
		if err != nil {
			return err
		}
		if req.ModelID != nil {
			if s.model(*req.ModelID) == nil {
				return fmt.Errorf("%w：机型不存在", ErrInvalid)
			}
			p.ModelID = *req.ModelID
		}
		if req.ProxyID != nil {
			if *req.ProxyID != "" && *req.ProxyID != DirectDevProxyID && st.Proxy(*req.ProxyID) == nil {
				return fmt.Errorf("%w：代理不存在", ErrInvalid)
			}
			p.ProxyID = *req.ProxyID
		}
		if req.GroupID != nil {
			p.GroupID = *req.GroupID
		}
		if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
			p.Name = strings.TrimSpace(*req.Name)
		}
		if req.Note != nil {
			p.Note = *req.Note
		}
		for key, fs := range req.Fields {
			f, ok := s.cat.Field(key)
			if !ok {
				return fmt.Errorf("%w：没有字段 %q", ErrInvalid, key)
			}
			if f.Readonly {
				continue
			}
			if !contains(f.Modes, fs.Mode) {
				return fmt.Errorf("%w：字段 %s 不支持模式 %q", ErrInvalid, key, fs.Mode)
			}
			p.Fields[key] = fs
		}
		return nil
	})
}

// Renew 一键新机：所有「随机」模式、没锁定的字段重新生成（不改机型）。
func (s *Service) Renew(id string) error {
	return s.store.Update(func(st *store.State) error {
		p, err := st.Phone(id)
		if err != nil {
			return err
		}
		m := s.model(p.ModelID)
		for key, fs := range p.Fields {
			if fs.Mode == "random" && !fs.Locked {
				fs.Value = RandomValue(key, m)
				p.Fields[key] = fs
			}
		}
		return nil
	})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
