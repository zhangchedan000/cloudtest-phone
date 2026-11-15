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
	streams  streamTracker
	usage    tracker[docker.Usage]
	netStat  tracker[NetStatus] // 上网健康检查（netcheck.go）
	batt     tracker[batteryState] // 电池漂移状态（battery.go）
	settings settingsTracker
	// netGuard 网络模块开着时检查这台设备有没有出口；返回错误就禁止开机（绝不走宿主机网络）。
	netGuard func(id string) error
	fakes    fakeTracker
	// async 为 false 时伪造文件同步挂载（测试用）。
	async bool
}

func NewService(cfg Config, d Docker, st *store.Store, cat *Catalog, models []Model) *Service {
	return &Service{cfg: cfg, docker: d, store: st, cat: cat, models: models, now: time.Now, run: execRunner{}, raw: execRaw{}, async: true}
}

// SetNetworkGuard 接上网络模块（mihomo）的开机前检查。
func (s *Service) SetNetworkGuard(fn func(id string) error) { s.netGuard = fn }

// NetworkEnabled 网络模块（mihomo）开没开。没开时云手机直接用宿主机网络，只适合开发测试。
func (s *Service) NetworkEnabled() bool { return s.netGuard != nil }

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
			ProxyIDs:       nonEmpty(req.ProxyID),
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

// EnsureNetwork 建好容器网络（网络模块的 DNS 要监听容器网关，所以管家程序启动时就建，不等第一次开机）。
func (s *Service) EnsureNetwork(ctx context.Context) error {
	return s.docker.EnsureNetwork(ctx, s.cfg.Network, s.cfg.Subnet, s.gateway())
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
		case f.Sink.Kind == "setting":
			// 开机后用安卓 settings put 写（settings.go）。个别值（如 android_id）
			// 系统可能另有机制，是否真生效以实机 settings get 为准。
			rf.Applied = true
			rf.Reason = "开机后设置"
		case f.Sink.Kind == "battery":
			// 开机后用 dumpsys battery 覆盖（settings.go）。
			rf.Applied = true
			rf.Reason = "开机后设置"
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
		// 指纹相关字段由 fingerprintProps 统一处理（保证各分区一致），这里不重复设。
		if r.Key == "fingerprint" || r.Key == "buildId" || r.Key == "incremental" {
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
	args = append(args, props...)
	args = append(args, disguiseProps()...)
	// 按手机的安卓版本取真实指纹（版本必须一致）。
	var realFP string
	if m := s.model(p.ModelID); m != nil {
		realFP = m.Fingerprints[fmt.Sprintf("%d", p.AndroidVersion)]
	}
	return append(args, fingerprintProps(byKey, realFP, p.AndroidVersion)...)
}

// redroidBuildID 各安卓版本的 Redroid 镜像自带的 buildId / incremental（真实值，
// 从镜像 /system/build.prop 读出来：docker create <镜像> + docker cp /system/build.prop）。
// 换镜像版本要重新读、更新这里；host/check-tells.sh 会暴露不一致。
// incremental 是 eng.* 的工程编译痕迹，没有真机真实值之前先沿用（不编造）。
var redroidBuildID = map[int]struct{ buildID, incremental string }{
	14: {"UD2A.240505.001.W1", "eng.frank.20240527.152423"},
	15: {"BP1A.250505.005.D1", "eng.root"},
}

// fingerprintProps 把各分区指纹和相关 build 字段（含 ro.product.name）统一覆盖成一致的值，
// 擦掉 Redroid 默认里的 redroid / redroid_arm64_only / userdebug / test-keys / eng.* / frank / build1。
// 指纹格式：brand/product/device:release/id/incremental:type/tags。
//   - 机型档案填了真实 stock 指纹（fingerprint 字段，从设备树/固件抠）就用真的，各字段从里面拆；
//   - 没填就用 Redroid 镜像的真实 buildId/incremental 兜底拼一个一致的（incremental 仍是 eng.*）。
// 注意：不设带空格的值（启动参数遇空格会截断）——ro.build.description 带空格，留作残留（很少被读）。
func fingerprintProps(byKey map[string]any, realFP string, androidVer int) []string {
	fp := realFP
	product, buildID, incremental, ok := parseFingerprint(fp)
	// 真指纹的安卓版本必须和手机一致，否则 release 对不上，宁可回退。
	if ok && !strings.HasPrefix(strings.SplitN(fp, ":", 3)[1], fmt.Sprintf("%d/", androidVer)) {
		ok = false
	}
	if !ok {
		bi, ridOK := redroidBuildID[androidVer]
		brand, device := str(byKey["brand"]), str(byKey["device"])
		if !ridOK || brand == "" || device == "" {
			return nil // 既没（版本相符的）真指纹、也没兜底数据：不覆盖，免得拼出错的
		}
		product, buildID, incremental = device, bi.buildID, bi.incremental
		fp = fmt.Sprintf("%s/%s/%s:%d/%s/%s:user/release-keys", brand, product, device, androidVer, buildID, incremental)
	}
	var out []string
	for _, name := range []string{
		"ro.build.fingerprint",
		"ro.system.build.fingerprint", "ro.vendor.build.fingerprint",
		"ro.product.build.fingerprint", "ro.odm.build.fingerprint",
		"ro.bootimage.build.fingerprint",
	} {
		out = append(out, name+"="+fp)
	}
	out = append(out,
		"ro.product.name="+product, // 真实产品名，如 xaga_global（默认是 redroid_arm64_only）
		"ro.build.id="+buildID,
		"ro.build.display.id="+buildID,
		"ro.build.version.incremental="+incremental,
		"ro.build.flavor="+product+"-user",
		"ro.build.host=android-build",
		"ro.build.user=android",
	)
	return out
}

// parseFingerprint 拆 brand/product/device:release/id/incremental:type/tags，
// 返回 product、buildId、incremental。格式不对或为空返回 ok=false。
func parseFingerprint(fp string) (product, buildID, incremental string, ok bool) {
	colon := strings.SplitN(fp, ":", 3) // [brand/product/device, release/id/incremental, type/tags]
	if len(colon) != 3 {
		return "", "", "", false
	}
	left := strings.Split(colon[0], "/") // brand, product, device
	mid := strings.Split(colon[1], "/")  // release, id, incremental
	if len(left) != 3 || len(mid) != 3 {
		return "", "", "", false
	}
	product, buildID, incremental = left[1], mid[1], mid[2]
	if product == "" || buildID == "" || incremental == "" {
		return "", "", "", false
	}
	return product, buildID, incremental, true
}

// disguiseProps 所有云手机都加的固定属性：让容器看起来像一台「锁了 bootloader 的正式版」手机，
// 而不是 Redroid 默认的 userdebug / test-keys / 未验证启动。
//
// 只放不影响开机的属性。ro.hardware / ro.board.platform 不在这里——安卓按它俩加载硬件驱动，
// 用启动参数改会开不了机（见 profiles/fields.json 的 boardPlatform 字段，要编译镜像时一起改）。
// SELinux 状态由宿主机内核决定（这台 Ubuntu 没开 SELinux），容器里改不动，也不在这里。
//
// 这些是通用伪装、不区分机型，所以写在代码里而不是机型档案里。改完重启生效。
func disguiseProps() []string {
	return []string{
		// 调试 / 签名：Redroid 镜像是 userdebug + test-keys，真机出厂是 user + release-keys。
		"ro.debuggable=0",
		"ro.build.type=user",
		"ro.build.tags=release-keys",
		// 启动校验：真机锁了 bootloader、开了 verity，这几项是 Play Integrity / root 检测会看的。
		"ro.boot.verifiedbootstate=green",
		"ro.boot.flash.locked=1",
		"ro.boot.veritymode=enforcing",
		"ro.boot.vbmeta.device_state=locked",
		"ro.secure=1",
	}
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
	if s.netGuard != nil {
		if err := s.netGuard(id); err != nil {
			return fmt.Errorf("%w：%v", ErrBlocked, err)
		}
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
	// 先标成「设置中」，开机后设置做完之前设备都算开机中（不会在框架重启前就让人操作）。
	s.settings.set(p.ID, SettingsStatus{State: "pending"})
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
	// Network 网络模块开着时，这台设备为什么没有出口（空 = 正常或网络模块没开）。
	Network string `json:"network,omitempty"`
	// Usage 最近一次采到的 CPU、内存（运行中的设备才有）。
	Usage *docker.Usage `json:"usage,omitempty"`
	// Net 最近一次上网检查（运行中的设备才有）。
	Net *NetStatus `json:"net,omitempty"`
}

func (s *Service) List(ctx context.Context) []PhoneView {
	st := s.store.Snapshot()
	out := make([]PhoneView, 0, len(st.Phones))
	for _, p := range st.Phones {
		p.Status = s.State(ctx, p.ID)
		view := PhoneView{Phone: p, Issues: s.Check(p, st), Resolved: s.resolve(p, st), Fakes: s.fakes.get(p.ID), Settings: s.settings.get(p.ID)}
		if s.netGuard != nil {
			if err := s.netGuard(p.ID); err != nil {
				view.Network = err.Error()
			}
		}
		if u := s.usage.get(p.ID); u != nil && p.Status != "stopped" && u.MemLimitMB > 0 {
			view.Usage = u
		}
		if n := s.netStat.get(p.ID); n != nil && p.Status == "running" {
			view.Net = n
		}
		out = append(out, view)
	}
	return out
}

// SaveFields 保存参数（和可选的机型、代理、分组、名称）。
type SaveRequest struct {
	Fields  map[string]store.FieldSetting `json:"fields"`
	ModelID *string                       `json:"modelId"`
	ProxyID *string                       `json:"proxyId"`
	// ProxyIDs 这台挂的代理列表（可以切换）；当前在用的不在列表里时改成列表第一个。
	ProxyIDs *[]string `json:"proxyIds"`
	GroupID  *string   `json:"groupId"`
	Name     *string   `json:"name"`
	Note     *string   `json:"note"`
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
			if p.ProxyID != "" && len(p.ProxyIDs) > 0 && !contains(p.ProxyIDs, p.ProxyID) {
				p.ProxyIDs = append([]string{p.ProxyID}, p.ProxyIDs...)
			}
		}
		if req.ProxyIDs != nil {
			var list []string
			for _, pid := range *req.ProxyIDs {
				if pid == "" || contains(list, pid) {
					continue
				}
				if pid != DirectDevProxyID && st.Proxy(pid) == nil {
					return fmt.Errorf("%w：代理 %q 不存在", ErrInvalid, pid)
				}
				list = append(list, pid)
			}
			p.ProxyIDs = list
			if len(list) > 0 && !contains(list, p.ProxyID) {
				p.ProxyID = list[0]
			}
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

// nonEmpty 一个元素的列表；空字符串返回 nil。
func nonEmpty(v string) []string {
	if v == "" {
		return nil
	}
	return []string{v}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
