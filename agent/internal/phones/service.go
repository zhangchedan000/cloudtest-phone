// Package phones 是管家程序的核心：创建、开机、关机、删除云手机，把参数落到安卓里。
package phones

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/docker"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
	"github.com/zhangchedan000/prism-phone/agent/internal/wifi"
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
	HostID      string
	HostName    string
	GPUFamily   string         // "software" 表示没有显卡（开发机）
	GPUMode     string         // Redroid 的 gpu_mode：guest（软件渲染）/ host
	Images      map[int]string // 安卓版本 → 镜像
	GappsImages map[int]string // 安卓版本 → 带谷歌服务（GMS）的镜像；空 = 这台机器不支持谷歌服务
	DataDir     string         // 每台云手机的 /data 放在 DataDir/phones/<id>/data
	Network     string         // 容器网络名
	Subnet      string         // 例如 10.88.0.0/16
	MemoryMB    int64          // 每台容器内存上限（保护宿主机）
	CPUs        float64        // 每台容器 CPU 上限
	MaxRunning  int            // 同时最多运行几台
	// DevHost 开发机：允许「直连（仅开发）」代理，方便没代理时测试。正式机器必须是 false。
	DevHost bool
	// MemFollowModel 容器内存上限按机型 memTotal（memory.go）；false 时用固定的 MemoryMB。
	MemFollowModel bool
	// HostMemMB 宿主机内存（0 = 自动读，测试用）。Lxcfs 宿主机装了 lxcfs：手机的 /proc/meminfo 按容器显示。
	HostMemMB int64
	Lxcfs     bool
	// Wifi 宿主机能做虚拟 WiFi（hwsim、hostapd、dnsmasq 都在）；WifiDNS 手机 WiFi 里的 DNS 转到哪（mihomo DNS）。
	Wifi bool
	// Sensors 镜像带传感器 HAL 时给手机喂传感器数据（踩坑 #59）
	Sensors bool
	// MemOvercommit 内存超卖倍数（默认 1 = 每台的上限加起来不超过机器内存）。板子上手机平时只用 1~2GB，
	// 设成 2.5 左右能多开几台；手机里显示的内存不变（按机型）。超卖时开机前还会看机器实际剩多少。
	MemOvercommit float64
	// HostGuard 手机开机后把被改掉的宿主机内核设置改回来（hostguard，踩坑 #58）
	HostGuard bool
	WifiDNS   string
	// DataImages 新手机的 /data 用按机型容量建的 ext4 磁盘镜像（手机里看到的总容量对得上机型，storage.go）。
	DataImages bool
	// InitHelper pp-init 启动器（管家程序自己的路径）：喂传感器/RIL + 开机时长归零都靠它；空 = 不用。
	InitHelper string
	// Timens 是否建时间命名空间让开机时长归零。false 时 pp-init 照常喂传感器/RIL，只是开机时长不归零
	//（有的板子内核 CLONE_NEWTIME 有问题，exec 后建线程失败，踩坑 #98）。
	Timens bool
	// HostCPUs 宿主机核数（0 = 自动）；BootTimeout / PollEvery 等开机的超时和间隔（0 = 默认）。
	HostCPUs    int
	BootTimeout time.Duration
	PollEvery   time.Duration
}

const DirectDevProxyID = "direct-dev"

// binderSlots 多开 binder 槽位数：没有 binderfs 的板子（如魔云腾 Q1）有 36 个静态 binder 设备，
// 每台手机占一组 3 个（binder/hwbinder/vndbinder），所以最多 12 台同时跑（上板流程 阶段3）。
const binderSlots = 12

// assignBinderSlot 给要开机的手机分一组没人用的 binder 设备，返回槽位号和要加进容器的 docker bind
// （把宿主 /dev/binderN 直接绑进容器的 /dev/binder、/dev/hwbinder、/dev/vndbinder）。
// 多台 --privileged 容器默认都看到宿主同一个 /dev/binder（binder1），会抢冲突；给每台绑不同 binderN。
// 已用槽位从「正在跑的容器」的 prism-phone.binder 标签反推（docker 是真相，agent 重启也不会撞）。
// 用 docker bind 而不是容器内 mount：不依赖 pp-init/时间命名空间（这块板子内核的 CLONE_NEWTIME 会让
// 安卓建线程失败，见踩坑；binderfs 的板子容器内各自挂 binderfs，这里分到的 binderN 它也用不上、无害）。
func (s *Service) assignBinderSlot(ctx context.Context, st store.State, id string, reserved ...int) (int, []string) {
	used := map[int]bool{}
	for _, k := range reserved {
		used[k] = true
	}
	for _, other := range st.Phones {
		if other.ID == id || !s.isRunning(ctx, other.ID) {
			continue
		}
		info, err := s.docker.InspectContainer(ctx, containerName(other.ID))
		if err != nil || info == nil {
			continue
		}
		if v := info.Config.Labels["prism-phone.binder"]; v != "" {
			if k, e := strconv.Atoi(v); e == nil {
				used[k] = true
			}
		}
	}
	slot := -1
	for k := 0; k < binderSlots; k++ {
		if !used[k] {
			slot = k
			break
		}
	}
	if slot < 0 {
		log.Printf("手机 %s：binder 槽位用尽（最多 %d 台多开），降级共用 binder1（可能和别台冲突）", id, binderSlots)
		slot = 0
	}
	// 第 k 组 = binder(3k+1), binder(3k+2), binder(3k+3)
	n := slot * 3
	binds := []string{
		fmt.Sprintf("/dev/binder%d:/dev/binder", n+1),
		fmt.Sprintf("/dev/binder%d:/dev/hwbinder", n+2),
		fmt.Sprintf("/dev/binder%d:/dev/vndbinder", n+3),
	}
	return slot, binds
}

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
	screens  screenCache // 横竖屏（rotate.go）
	usage    tracker[docker.Usage]
	dataJobs tracker[DataJob]
	netStat  tracker[NetStatus]    // 上网健康检查（netcheck.go）
	batt     tracker[batteryState] // 电池漂移状态（battery.go）
	settings settingsTracker
	// netGuard 网络模块开着时检查这台设备有没有出口；返回错误就禁止开机（绝不走宿主机网络）。
	netGuard func(id string) error
	// netPeek 只读网络模块的现状（不触发同步），列表页每 5 秒刷新用；没接就退回 netGuard。
	netPeek func(id string) error
	// starting 正在开机、容器还没建好的手机占的 binder 槽位和内存（防两台同时开机抢同一组 binder、绕过台数/内存上限）。
	startMu  sync.Mutex
	starting map[string]startReservation
	// closeConns 断开某个来源 IP 的所有代理连接（切换代理后让长连接改走新节点，踩坑 #70）
	closeConns func(ctx context.Context, ip string) (int, error)
	// proxyChecker 检测代理出口（时区 / 语言跟随代理要用），见 proxyauto.go。
	proxyChecker func(ctx context.Context, proxyID string) error
	fakes        fakeTracker
	// newRadio 建 hwsim 虚拟无线网卡（测试时换成假的）
	newRadio wifi.NewRadioFunc
	// async 为 false 时伪造文件同步挂载（测试用）。
	async bool
}

func NewService(cfg Config, d Docker, st *store.Store, cat *Catalog, models []Model) *Service {
	return &Service{cfg: cfg, docker: d, store: st, cat: cat, models: models, now: time.Now, run: execRunner{}, raw: execRaw{}, async: true, newRadio: wifi.NewRadioInNetns}
}

// SetNetworkGuard 接上网络模块（mihomo）的开机前检查。
func (s *Service) SetNetworkGuard(fn func(id string) error) { s.netGuard = fn }

// SetNetworkPeek 接上只读的网络状态查询（不触发 mihomo 同步），列表页用。
func (s *Service) SetNetworkPeek(fn func(id string) error) { s.netPeek = fn }

// startReservation 一台正在开机的手机预占的资源。
type startReservation struct {
	slot  int
	memMB int64
}

// reserveStart 在锁里一次性做完「台数上限 → 分 binder 槽位 → 内存预算」并记下预占，
// 返回的 release 在开机流程结束（容器已建好，或失败）时调用。
func (s *Service) reserveStart(ctx context.Context, st store.State, id string, memLimit int64) (int, []string, func(), error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.starting == nil {
		s.starting = map[string]startReservation{}
	}
	if _, busy := s.starting[id]; busy {
		return 0, nil, nil, fmt.Errorf("%w：这台正在开机，等一下", ErrBlocked)
	}
	if s.cfg.MaxRunning > 0 {
		running := 0
		for _, other := range st.Phones {
			if other.ID == id {
				continue
			}
			if _, pending := s.starting[other.ID]; pending || s.isRunning(ctx, other.ID) {
				running++
			}
		}
		if running >= s.cfg.MaxRunning {
			return 0, nil, nil, fmt.Errorf("%w：这台机器同时最多运行 %d 台，先关掉一台", ErrBlocked, s.cfg.MaxRunning)
		}
	}
	var pendingSlots []int
	var pendingMem int64
	for other, r := range s.starting {
		if other != id {
			pendingSlots = append(pendingSlots, r.slot)
			pendingMem += r.memMB
		}
	}
	slot, binds := s.assignBinderSlot(ctx, st, id, pendingSlots...)
	if err := s.checkMemoryBudget(ctx, id, memLimit+pendingMem); err != nil {
		return 0, nil, nil, err
	}
	s.starting[id] = startReservation{slot: slot, memMB: memLimit}
	release := func() {
		s.startMu.Lock()
		delete(s.starting, id)
		s.startMu.Unlock()
	}
	return slot, binds, release, nil
}

// SetConnectionCloser 网络模块断开某台手机所有连接的函数（main 里接 network.Syncer.CloseConnections）。
func (s *Service) SetConnectionCloser(fn func(ctx context.Context, ip string) (int, error)) {
	s.closeConns = fn
}

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
	Gapps          bool   `json:"gapps"` // 带谷歌服务（GMS / Play 商店）
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
	if req.Gapps && s.cfg.GappsImages[req.AndroidVersion] == "" {
		return store.Phone{}, fmt.Errorf("%w：这台机器没装 Android %d 的谷歌服务镜像（先运行 host/build-gapps-image.sh）", ErrInvalid, req.AndroidVersion)
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
			Gapps:          req.Gapps,
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
			// 实测：Redroid 启动参数里的值遇到空格会被截断（「POCO X4 GT」变成「POCO」），
			// 所以不走启动参数，改由开机前挂进去的 /system/build.prop 带进去（buildprop.go，踩坑 #55）。
			rf.Applied = true
			rf.Reason = "写进 build.prop（值里有空格，启动参数传不了）"
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
	// 2026-11-17 补热门国家
	"NZ": "en-NZ", "IE": "en-IE", "IN": "en-IN", "NL": "nl-NL", "PL": "pl-PL", "TR": "tr-TR", "RU": "ru-RU",
	"PT": "pt-PT", "AR": "es-AR", "CL": "es-CL", "CO": "es-CO", "MO": "zh-MO", "AE": "en-AE", "SA": "ar-SA",
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
	args, _ := s.bootProps(p, st)
	return args
}

// bootProps 启动参数 + 带空格、启动参数传不了的属性（spaced：市场名、ro.build.description……，
// 改由开机前挂进去的 build.prop 文件带进去，见 buildprop.go）。
func (s *Service) bootProps(p store.Phone, st store.State) ([]string, map[string]string) {
	spaced := map[string]string{}
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
		args = append(args, fmt.Sprintf("androidboot.redroid_fps=%d", redroidFPS(fps)))
	}
	// 真机分区身份（有的话）：手动 / 导入了指纹就不用（那是另一台真机的身份，不能混）。
	var realBP map[string]string
	if str(byKey["fingerprint"]) == "" {
		if m := s.model(p.ModelID); m != nil {
			realBP = m.BuildProps[fmt.Sprintf("%d", p.AndroidVersion)]
		}
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
			if _, real := realBP[name]; real {
				continue // 真机数据里有这个分区属性，用真机的
			}
			if strings.ContainsAny(val, " \t") {
				spaced[name] = val // 带空格（如市场名 Xiaomi 13T）：启动参数会截断，走 build.prop 文件
				continue
			}
			props = append(props, name+"="+val)
		}
	}
	sort.Strings(props)
	args = append(args, props...)
	args = append(args, disguiseProps()...)
	// ro.kernel.version 要和伪造的 uname 内核版本大小版本一致（2026-11-18 体检：露了宿主机的 6.17，
	// 真机该是 5.10 / 5.15 之类，和 uname 对得上）。取伪造内核版本号的「主.次」。
	if mm := kernelMajorMinor(str(byKey["kernelRelease"])); mm != "" {
		args = append(args, "ro.kernel.version="+mm)
	}
	// 指纹来源：手动填写 / 导入真机参数的优先（fingerprint 字段非空就用它），
	// 否则按手机安卓版本取机型库里的真实指纹（版本必须一致）。
	var realFP string
	if fp := str(byKey["fingerprint"]); fp != "" {
		realFP = fp
	} else if m := s.model(p.ModelID); m != nil {
		realFP = m.Fingerprints[fmt.Sprintf("%d", p.AndroidVersion)]
	}
	args = withBuildProps(append(args, fingerprintProps(byKey, realFP, p.AndroidVersion)...), realBP)
	args = partitionIdentity(args, realBP)
	for k, v := range realBP {
		if strings.HasPrefix(k, "ro.") && strings.ContainsAny(v, " \t") && !strings.Contains(k, "security_patch") {
			spaced[k] = v
		}
	}
	// 蓝牙名字（踩坑 #48）：蓝牙第一次打开时取 bluetooth.device.default_name（没有就用 ro.product.model），
	// 和设置里的设备名（deviceName）一致；之后蓝牙自己记住。走 build.prop（名字常带空格）。
	if name := strings.TrimSpace(str(byKey["deviceName"])); name != "" {
		spaced["bluetooth.device.default_name"] = name
	}
	// ro.build.description（flavor release id incremental tags，带空格）：真机数据里有就用，没有按最终值拼
	if _, ok := spaced["ro.build.description"]; !ok {
		final := map[string]string{}
		for _, a := range args {
			if k, v, ok := strings.Cut(a, "="); ok {
				final[k] = v
			}
		}
		if final["ro.build.flavor"] != "" && final["ro.build.id"] != "" && final["ro.build.version.incremental"] != "" {
			spaced["ro.build.description"] = fmt.Sprintf("%s %d %s %s release-keys", final["ro.build.flavor"], p.AndroidVersion, final["ro.build.id"], final["ro.build.version.incremental"])
		}
	}
	return args, spaced
}

// withBuildProps 用真机的分区属性替换兜底值：fingerprintProps 里同名的去掉，真机的整套加在后面；
// 真机数据里没有的（某些分区没有对应文件）仍保留兜底值，保证不漏 redroid 默认值。值带空格的跳过（启动参数会截断）。
// partitionIdentity 各分区的 ro.product.<分区>.brand/name/device 跟这个分区的指纹对上（2026-11-17 体检发现：
// 真机数据里 system 分区指纹是 Android/missi_phone_global/missi，ro.product.system.device 却还是镜像的 generic）。
// 指纹格式 品牌/产品名/设备名:版本/…；真机数据里本来就有的 ro.product.<分区>.* 不动。
func partitionIdentity(args []string, realBP map[string]string) []string {
	final := map[string]string{}
	for _, a := range args {
		if k, v, ok := strings.Cut(a, "="); ok {
			final[k] = v
		}
	}
	set := map[string]string{}
	for _, part := range []string{"system", "vendor", "odm", "product", "system_ext", "vendor_dlkm", "odm_dlkm", "system_dlkm"} {
		fp := final["ro."+part+".build.fingerprint"]
		head, _, ok := strings.Cut(fp, ":")
		f := strings.Split(head, "/")
		if !ok || len(f) != 3 || f[0] == "" || f[1] == "" || f[2] == "" {
			continue
		}
		for i, field := range []string{"brand", "name", "device"} {
			k := "ro.product." + part + "." + field
			// 指纹本来就是按 品牌/产品名/设备名 拼出来的，所以这三个字段必须和指纹对上。
			// 真机数据里跟指纹一致的不动（省得重复加）；对不上的（比如小米 mainline 镜像把
			// system.device 写成 generic、system.name 写成 mainline，都和指纹里的 missi / missi_phone_global
			// 对不上，是差异项）用指纹里的盖掉；真机数据里没有的也补上。
			if rv, real := realBP[k]; !real || rv != f[i] {
				set[k] = f[i]
			}
		}
	}
	if len(set) == 0 {
		return args
	}
	out := make([]string, 0, len(args)+len(set))
	for _, a := range args {
		k, _, _ := strings.Cut(a, "=")
		if _, ok := set[k]; !ok {
			out = append(out, a)
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+set[k])
	}
	return out
}

func withBuildProps(base []string, bp map[string]string) []string {
	if len(bp) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(bp))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, real := bp[k]; !real {
			out = append(out, kv)
		}
	}
	keys := make([]string, 0, len(bp))
	for k, v := range bp {
		// 安全补丁日期不照搬（比镜像旧会让密钥库认为补丁回退，已存的密钥失效）。
		if v != "" && !strings.ContainsAny(v, " \t\n") && strings.HasPrefix(k, "ro.") && !strings.Contains(k, "security_patch") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+bp[k])
	}
	return out
}

// redroidBuildID 各安卓版本的 Redroid 镜像自带的 buildId / incremental（真实值，
// 从镜像 /system/build.prop 读出来：docker create <镜像> + docker cp /system/build.prop）。
// 换镜像版本要重新读、更新这里；host/check-tells.sh 会暴露不一致。
// incremental 是 eng.* 的工程编译痕迹，没有真机真实值之前先沿用（不编造）。
var redroidBuildID = map[int]struct{ buildID, incremental string }{
	13: {"TQ3A.230901.001", "9820097"}, // 板子 lineage-20 是安卓13：没这条就一进兜底 return nil、镜像 redroid 值全漏（踩坑 #104）
	14: {"UD2A.240505.001.W1", "eng.frank.20240527.152423"},
	15: {"BP1A.250505.005.D1", "eng.root"},
}

// fingerprintProps 把各分区指纹和相关 build 字段（含 ro.product.name）统一覆盖成一致的值，
// 擦掉 Redroid 默认里的 redroid / redroid_arm64_only / userdebug / test-keys / eng.* / frank / build1。
// 指纹格式：brand/product/device:release/id/incremental:type/tags。
//   - 机型档案填了真实 stock 指纹（fingerprint 字段，从设备树/固件抠）就用真的，各字段从里面拆；
//   - 没填就用 Redroid 镜像的真实 buildId/incremental 兜底拼一个一致的（incremental 仍是 eng.*）。
//
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
	brand, manu, model, device := str(byKey["brand"]), str(byKey["manufacturer"]), str(byKey["model"]), str(byKey["device"])
	// 主 build 属性。
	out := []string{
		"ro.build.fingerprint=" + fp,
		"ro.product.name=" + product, // 真实产品名，如 xaga_global（默认是 redroid_arm64_only）
		"ro.build.id=" + buildID,
		"ro.build.display.id=" + buildID,
		"ro.build.version.incremental=" + incremental,
		"ro.build.flavor=" + product + "-user",
		"ro.build.host=android-build",
		"ro.build.user=android",
	}
	// 每个分区的 build 子属性全覆盖：不光主 fingerprint，连各分区的 build.id / tags / type /
	// incremental 也要盖，否则默认镜像残留的 redroid / redroid_arm64_only / UD2A.* /
	// userdebug / test-keys / eng.frank 会从 system_ext / vendor_dlkm / odm_dlkm 等分区漏出去
	// （魔云腾改机 SDK 也处理 vendor_dlkm/odm_dlkm 分区，漏了会被查出，2026-11-15 实测发现）。
	for _, p := range []string{"system", "vendor", "odm", "product", "system_ext", "vendor_dlkm", "odm_dlkm", "system_dlkm", "bootimage"} {
		out = append(out,
			"ro."+p+".build.fingerprint="+fp,
			"ro."+p+".build.id="+buildID,
			"ro."+p+".build.tags=release-keys",
			"ro."+p+".build.type=user",
			"ro."+p+".build.version.incremental="+incremental,
		)
	}
	// 主分区的 ro.product.*.name：机型字段设了 .device 但没设 .name，默认还是 redroid_arm64_only，要补。
	for _, p := range []string{"system", "vendor", "odm", "product"} {
		out = append(out, "ro.product."+p+".name="+product)
	}
	// 各分区的 ro.product.* 身份：system/vendor/odm/product 由机型字段已设，
	// 这里补 system_ext/vendor_dlkm/odm_dlkm/system_dlkm 这几个漏掉的分区（默认全是 redroid）。
	for _, p := range []string{"system_ext", "vendor_dlkm", "odm_dlkm", "system_dlkm"} {
		if brand != "" {
			out = append(out, "ro.product."+p+".brand="+brand)
		}
		if manu != "" {
			out = append(out, "ro.product."+p+".manufacturer="+manu)
		}
		if model != "" {
			out = append(out, "ro.product."+p+".model="+model)
		}
		if device != "" {
			out = append(out, "ro.product."+p+".device="+device)
		}
		out = append(out, "ro.product."+p+".name="+product)
	}
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
		// 开机模式：Redroid 没设 androidboot.mode，init 就把 ro.bootmode 置成 unknown；真机正常开机是 normal
		// （2026-11-18 体检：ro.bootmode=unknown）。设 ro.boot.mode，ro.bootmode 跟着变 normal。
		"ro.boot.mode=normal",
		// 设备类型：Redroid 是 default（模拟器 / 通用），手机出厂多是 nosdcard。
		"ro.build.characteristics=nosdcard",
		// CPU 类型：Redroid 是 generic（模拟器 / 通用编译），真机写具体核心。开发机 Neoverse N1 和板子 RK3588
		// 都是 Cortex-A76 一类，指令集一致（dotprod / fp16），按 a76 编译不会出错。
		// dalvik.vm.isa.*.variant 不是只读属性，开机参数会被 build.prop 盖回去，在开机设置里 setprop（cpuVariantProps）。
		"ro.bionic.cpu_variant=" + cpuVariant,
		"ro.bionic.2nd_cpu_variant=" + cpuVariant, // 2026-11-17 体检：还是 generic
	}
}

// kernelMajorMinor 从伪造的内核版本号里取「主.次」，如 "5.10.187-android12-9" → "5.10"。拿不到返回 ""。
func kernelMajorMinor(rel string) string {
	rel = strings.TrimSpace(rel)
	f := strings.SplitN(rel, ".", 3)
	if len(f) < 2 || f[0] == "" || f[1] == "" {
		return ""
	}
	for _, c := range f[0] + f[1] {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return f[0] + "." + f[1]
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

// imageFor 选这台手机开机用的镜像：带谷歌服务的用谷歌服务镜像，否则用干净 AOSP 镜像。
func (s *Service) imageFor(p store.Phone) (string, error) {
	if p.Gapps {
		if img := s.cfg.GappsImages[p.AndroidVersion]; img != "" {
			return img, nil
		}
		return "", fmt.Errorf("%w：这台机器没装 Android %d 的谷歌服务镜像（先运行 host/build-gapps-image.sh）", ErrInvalid, p.AndroidVersion)
	}
	if img := s.cfg.Images[p.AndroidVersion]; img != "" {
		return img, nil
	}
	return "", fmt.Errorf("%w：这台机器没有 Android %d 的镜像", ErrInvalid, p.AndroidVersion)
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
	}
	// 注：透明代理（tproxy → mihomo → 节点）在开发机上实测已通，分配健康真实代理即走节点 IP，
	// 不再有「还没接上」的提醒（2026-11-15 端到端验证：手机出口 IP = 节点 IP）。
	if p.Gapps && s.cfg.GappsImages[p.AndroidVersion] == "" {
		issues = append(issues, Issue{"error", "gapps-image-missing", fmt.Sprintf("这台机器没装 Android %d 的谷歌服务镜像，无法开「带谷歌服务」的手机。", p.AndroidVersion)})
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
	// 时区 / 语言跟随代理：代理没检测过就先检测，再拿最新数据算开机参数。
	if p.ProxyID != "" {
		s.ensureProxyChecked(ctx, p.ProxyID)
		st = s.store.Snapshot()
		if p, err = st.Phone(id); err != nil {
			return err
		}
	}
	byKey := map[string]any{}
	for _, r := range s.resolve(*p, st) {
		byKey[r.Key] = r.Value
	}
	memLimit := s.memLimitMB(byKey)
	// 台数上限、binder 槽位（docker bind 进容器，见 assignBinderSlot）、内存预算在一把锁里分配并预占，
	// 直到容器建好才释放——两台同时开机不会拿到同一组 binder。
	binderSlot, binderBinds, release, err := s.reserveStart(ctx, st, id, memLimit)
	if err != nil {
		return err
	}
	defer release()
	if err := s.docker.EnsureNetwork(ctx, s.cfg.Network, s.cfg.Subnet, s.gateway()); err != nil {
		return fmt.Errorf("准备网络：%w", err)
	}
	dataDir, err := s.prepareData(ctx, p.ID, byKey)
	if err != nil {
		return err
	}
	binds := []string{dataDir + ":/data"}
	binds = append(binds, binderBinds...) // 多开 binder：这台专属的一组 binder 设备
	if s.cfg.Lxcfs {
		binds = append(binds, lxcfsProc+":"+lxcfsMount+":ro")
	}
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
	image, err := s.imageFor(*p)
	if err != nil {
		return err
	}
	bootArgs, spaced := s.bootProps(*p, st)
	// 硬件名（ro.hardware）：镜像里按芯片名放好了显卡驱动（标签 prism.hw）就用机型的芯片名，不再是 redroid（踩坑 #63）。
	// 启动参数在入口里改（withHardware）；这里只给改 build.prop 用。
	hw := s.hardwareName(ctx, image, s.resolvedValues(*p, st))
	propArgs := bootArgs
	if hw != "" {
		propArgs = append(append([]string{}, bootArgs...), "ro.hardware="+hw)
	}
	if bpBinds, err := s.prepareBuildProps(ctx, p.ID, image, propArgs, spaced); err != nil {
		log.Printf("手机 %s：build.prop 没改写（照镜像原样开机）：%v", p.ID, err)
	} else {
		binds = append(binds, bpBinds...)
	}
	entrypoint := s.initEntrypoint(ctx, image)
	if hw != "" {
		entrypoint = withHardware(entrypoint, s.imageEntrypoint(ctx, image), hw)
	}
	if entrypoint != nil && s.cfg.Sensors && s.imageHasSensors(ctx, image) {
		byKey := s.resolvedValues(*p, st)
		spec := sensorSpec(*p, st, byKey)
		if s.imageLabel(ctx, image, "prism.ril") == "1" {
			spec.Modem = s.simSpec(*p, st, byKey)
			// IMEI 补丁（踩坑 #115）：把这台唯一 IMEI 打进 libril-modem-lib.so，盖掉镜像 getImei 的写死值。
			// 失败/定位不到（安卓15/16 立即数）只记日志、照原样开机，不挡开机、不影响老手机。
			if spec.Modem != nil && spec.Modem.IMEI != "" {
				if b, err := s.prepareImei(ctx, p.ID, image, spec.Modem.IMEI); err != nil {
					log.Printf("手机 %s：IMEI 补丁没打（getImei 仍是镜像默认值）：%v", p.ID, err)
				} else if b != "" {
					binds = append(binds, b)
				}
			}
		}
		spec.BT = btAvailable() && s.imageHasBT(ctx, image)
		spec.RealNames = s.imageLabel(ctx, image, "prism.names") == "1"
		entrypoint = withSensors(entrypoint, spec)
	}
	if entrypoint != nil {
		binds = append(binds, s.cfg.InitHelper+":"+initMount+":ro")
	}
	_, err = s.docker.CreateContainer(ctx, docker.ContainerSpec{
		Entrypoint: entrypoint,
		Name:       name,
		Image:      image,
		Cmd:        bootArgs,
		Labels:     map[string]string{"prism-phone": "1", "prism-phone.id": p.ID, "prism-phone.binder": strconv.Itoa(binderSlot)},
		Binds:      binds,
		MemoryMB:   memLimit,
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
	// 虚拟 WiFi 要在安卓的有线网管理启动之前把 eth0 换掉，所以容器一起来就同步做（wifi.go）
	s.setupWifi(ctx, p.ID, image, byKey)
	s.applyUname(ctx, p.ID, byKey)
	s.applyMounts(ctx, p.ID)
	s.guardLocalPorts(ctx, p.ID)
	if btAvailable() && s.cfg.Sensors && s.imageHasBT(ctx, image) {
		s.startBT(ctx, p.ID, byKey)
	}
	if s.async {
		go s.removeTraces(context.Background(), name)
	} else {
		s.removeTraces(ctx, name)
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
	name := containerName(id)
	// 体面关机：先广播关机 intent（App 有机会存数据）、sync 刷盘，再停容器。
	// 否则 docker stop 到点直接 SIGKILL，相当于硬断电，App 没存的数据会丢。
	// 尽力而为，失败忽略（可能本来就没在运行）；超时给够、再停容器时也多给几秒。
	gctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	_, _ = s.run.Run(gctx, "docker", "exec", name, "sh", "-c",
		"am broadcast -a android.intent.action.ACTION_SHUTDOWN >/dev/null 2>&1; sync")
	cancel()
	err := s.docker.StopContainer(ctx, name, 25)
	s.teardownWifi(ctx, id)
	s.stopBT(ctx, id)
	if docker.IsNotFound(err) {
		return nil
	}
	return err
}

// Delete 删除云手机。数据不直接删，挪到 DataDir/trash 下（误删还能找回），确认没问题再手动清。
func (s *Service) Delete(ctx context.Context, id string) error {
	// 先确认真有这台：id 会拼进宿主机路径，不认识的 id 一律不碰文件系统
	snap := s.store.Snapshot()
	if _, err := snap.Phone(id); err != nil {
		return err
	}
	if err := s.docker.RemoveContainer(ctx, containerName(id)); err != nil {
		return err
	}
	s.teardownWifi(ctx, id)
	s.stopBT(ctx, id)
	if err := s.releaseData(ctx, id); err != nil {
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
	// DataJob 正在做 / 最近做完的备份或恢复。
	DataJob *DataJob `json:"dataJob,omitempty"`
}

func (s *Service) List(ctx context.Context) []PhoneView {
	st := s.store.Snapshot()
	out := make([]PhoneView, 0, len(st.Phones))
	for _, p := range st.Phones {
		p.Status = s.State(ctx, p.ID)
		view := PhoneView{Phone: p, Issues: s.Check(p, st), Resolved: s.resolve(p, st), Fakes: s.fakes.get(p.ID), Settings: s.settings.get(p.ID), DataJob: s.dataJobs.get(p.ID)}
		if peek := s.netPeek; peek != nil {
			if err := peek(p.ID); err != nil {
				view.Network = err.Error()
			}
		} else if s.netGuard != nil {
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
	Gapps    *bool     `json:"gapps"` // 切换带谷歌服务（下次开机生效）
	Adb      *bool     `json:"adb"`   // 打开 / 关闭 USB 调试（开着的手机马上生效）
}

func (s *Service) Save(id string, req SaveRequest) error {
	err := s.save(id, req)
	if err == nil && req.Adb != nil {
		if s.async {
			go s.applyAdb(context.Background(), id)
		} else {
			s.applyAdb(context.Background(), id)
		}
	}
	return err
}

func (s *Service) save(id string, req SaveRequest) error {
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
		if req.Adb != nil {
			p.Adb = *req.Adb
		}
		if req.Gapps != nil {
			if *req.Gapps && s.cfg.GappsImages[p.AndroidVersion] == "" {
				return fmt.Errorf("%w：这台机器没装 Android %d 的谷歌服务镜像", ErrInvalid, p.AndroidVersion)
			}
			p.Gapps = *req.Gapps
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

// redroidFPS 给 Redroid 的刷新率。实测（踩坑 #44）：redroid_fps=144 时屏幕只报 15Hz，
// 60 / 120 正常；所以超过 120 的按 120 给（144Hz 屏的手机日常多数也跑 120）。
func redroidFPS(v any) int {
	n := 60
	switch x := v.(type) {
	case int:
		n = x
	case float64:
		n = int(x)
	case string:
		_, _ = fmt.Sscanf(x, "%d", &n)
	default:
		_, _ = fmt.Sscanf(fmt.Sprint(v), "%d", &n)
	}
	if n > 120 {
		n = 120
	}
	if n < 30 {
		n = 60
	}
	return n
}

// removeTraces 清掉容器痕迹：Redroid 镜像自带的 su（setuid root，检测 root 的第一项）和 Docker 的 /.dockerenv。
// 管家自己用 docker exec（本来就是 root），不需要 su。
// 刚启动时安卓的动态链接器（/apex）还没挂好，docker exec 跑不了任何命令（踩坑 #44），所以重试到成功为止，
// 一般开机后几秒就能删掉，早于 App 启动。
func (s *Service) removeTraces(ctx context.Context, name string) {
	// su / .dockerenv：动态链接器一起来就删（刚启动时 docker exec 跑不了命令，踩坑 #44，重试到成功）。
	deadline := time.Now().Add(s.bootTimeout())
	for {
		if _, err := s.run.Run(ctx, "docker", "exec", name, "sh", "-c", "rm -f /system/xbin/su /.dockerenv"); err == nil {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return
		}
		time.Sleep(time.Second)
	}
	// 启动器（/pp-init）开机后用不着了。喂数据进程现在从 memfd 跑、不再占着它（踩坑 #87），但容器里这个 bind
	// mount 要过一阵（有时几分钟）才卸得掉，卸掉之前 rm 一直报「忙」。所以单独盯久一点（最多 10 分钟，每 3 秒
	// 试一次），用退出码表示删没删掉（删掉 exit 0 就停）——否则偶尔残留一个 0 字节空文件（踩坑 #87 的尾巴）。
	// 这是在后台 goroutine 里跑的，盯久不影响开机；rm 很便宜、删过也没事。
	cmd := "umount -l " + initMount + " 2>/dev/null; rm -f " + initMount + " 2>/dev/null; [ -e " + initMount + " ] && exit 1 || exit 0"
	deadline = time.Now().Add(10 * time.Minute)
	for {
		if _, err := s.run.Run(ctx, "docker", "exec", name, "sh", "-c", cmd); err == nil {
			return
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return
		}
		time.Sleep(3 * time.Second)
	}
}

// cpuVariant 开发机 / 板子 CPU 对应的核心类型（见 disguiseProps）。
const cpuVariant = "cortex-a76"

// cpuVariantProps 开机后用 setprop 改的 CPU 类型属性（只改系统里本来就有的：64 位镜像没有 arm 32 位那个）。
var cpuVariantProps = []string{"dalvik.vm.isa.arm64.variant", "dalvik.vm.isa.arm.variant"}

// initMount 启动器在容器里的路径（开机后 removeTraces 卸掉删除）。
const initMount = "/data/.ppi" // 放 /data 下（App 列不了 /data）：/pp-init 以前靠 removeTraces 异步清，实测偶尔清不掉、0 字节空文件残留在根目录成差异项（踩坑 #93）。启动器在容器启动瞬间执行，早于 Android 把 /data 重挂 noexec，所以能跑

type imageInspector interface {
	ImageEntrypoint(ctx context.Context, image string) ([]string, error)
}

// initEntrypoint 用启动器包住镜像原来的入口，让安卓看到的开机时长从 0 开始（timens 包、踩坑 #49）。
// 没配启动器、读不到镜像入口时返回 nil（照旧用镜像入口开机，绝不因此开不了机）。
func (s *Service) initEntrypoint(ctx context.Context, image string) []string {
	if s.cfg.InitHelper == "" {
		return nil
	}
	ii, ok := s.docker.(imageInspector)
	if !ok {
		return nil
	}
	ep, err := ii.ImageEntrypoint(ctx, image)
	if err != nil || len(ep) == 0 {
		return nil
	}
	head := []string{initMount, "pp-init"}
	if !s.cfg.Timens {
		head = append(head, "--no-timens") // 不归零开机时长，但 pp-init 照常喂传感器/RIL（踩坑 #98）
	}
	head = append(head, "0")
	return append(head, ep...)
}
