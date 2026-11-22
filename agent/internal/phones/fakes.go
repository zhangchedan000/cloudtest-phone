package phones

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/fakefs"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 「内核层伪装」（不改内核）：开机完成后，把伪造的 /proc、/sys 文件 bind-mount 到容器里。
//
// 为什么等开机完成再挂：安卓 init 开机时会自己重新挂一遍 /proc，开机前挂的会被盖住。
// 伪造文件先放在容器里的临时挂载点 fakeMount，挂完就卸掉并删目录。
//
// 临时挂载点放在 /data 下（/data 权限 0771，App 列不了；根目录 App 能列）：即使卸载后 rmdir 没删掉、
// 留个空目录，App 也看不到（以前放在根目录 /.ppfake，开机后删不掉、成了破绽；2026-11 体检后搬进 /data）。
// mountinfo 里这些 bind mount 由内核模块 pp_mnt 过滤掉。

const fakeMount = "/data/.ppf"

// Runner 执行宿主机命令（测试时换成假的）。
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type execRunner struct{}

// ExecRunner 真正执行宿主机命令的 Runner（main 里给别的包用）。
func ExecRunner() Runner { return execRunner{} }

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultRunTimeout(name, args))
		defer cancel()
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// defaultRunTimeout 调用方没给超时时的兜底：安卓卡死（servicemanager / system_server 挂住）时
// docker exec 会永远等下去，协程泄漏、手机一直显示「开机中」。docker exec 给 5 分钟（够装大 APK），
// 其他宿主机命令（备份 tar / cp 大磁盘镜像）给 1 小时，只防永久卡死。
func defaultRunTimeout(name string, args []string) time.Duration {
	if name == "docker" && len(args) > 0 && args[0] == "exec" {
		return 5 * time.Minute
	}
	return time.Hour
}

// FakeStatus 一台设备伪造文件的挂载结果（给管理页看）。
type FakeStatus struct {
	State   string    `json:"state"` // pending / applied / failed
	Applied []string  `json:"applied,omitempty"`
	Skipped []string  `json:"skipped,omitempty"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
}

type fakeTracker struct {
	mu     sync.Mutex
	status map[string]FakeStatus
}

func (t *fakeTracker) set(id string, s FakeStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status == nil {
		t.status = map[string]FakeStatus{}
	}
	s.At = time.Now().UTC()
	t.status[id] = s
}

func (t *fakeTracker) get(id string) *FakeStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.status[id]; ok {
		return &s
	}
	return nil
}

func (s *Service) fakeDir(id string) string {
	return filepath.Join(s.cfg.DataDir, "phones", id, "fake")
}

// prepareFakes 开机前生成伪造文件。返回 false 表示这台设备没有可伪造的内容。
func (s *Service) prepareFakes(p store.Phone, st store.State) (bool, error) {
	byKey := map[string]any{}
	for _, r := range s.resolve(p, st) {
		byKey[r.Key] = r.Value
	}
	spec := fakefs.Spec{
		KernelRelease: str(byKey["kernelRelease"]),
		KernelBuild:   str(byKey["kernelBuild"]),
		HostCPUInfo:   readHost("/proc/cpuinfo"),
		BootID:        randUUID(), // 每次开机一个新的（踩坑 #79）
	}
	_ = convert(byKey["cpuParts"], &spec.CPUParts)
	_ = convert(byKey["cpuMaxFreq"], &spec.CPUMaxFreqKHz)
	// 宿主机核数比机型少时（开发机 4 核，手机 8 核），只写宿主机有的核，
	// 否则 cpuinfo 里 8 个核、/sys/devices/system/cpu/online 只有 0-3，自相矛盾。
	spec.CPUParts, spec.CPUMaxFreqKHz = fitCPUs(spec.CPUParts, spec.CPUMaxFreqKHz, s.hostCPUs())
	files, err := fakefs.Build(spec)
	if err != nil {
		return false, err
	}
	if len(files) == 0 {
		return false, nil
	}
	if err := fakefs.Write(s.fakeDir(p.ID), files); err != nil {
		return false, err
	}
	// 记下每个文件对应容器里的路径（内容不重复存）。
	manifest := make([]fakefs.File, len(files))
	for i, f := range files {
		manifest[i] = fakefs.File{Name: f.Name, Target: f.Target}
	}
	raw, _ := json.MarshalIndent(manifest, "", "  ")
	return true, os.WriteFile(filepath.Join(s.fakeDir(p.ID), "..", "fake-targets.json"), raw, 0o644)
}

func (s *Service) hostCPUs() int {
	if s.cfg.HostCPUs > 0 {
		return s.cfg.HostCPUs
	}
	return runtime.NumCPU()
}

// fitCPUs 把机型的核心分组按比例缩到宿主机核数，每组至少留 1 个（核太少时优先留大核）。
// 以前从前往后截，8 核的天玑 / 骁龙在 4 核开发机上只剩 4 个 A55 小核，像一台低端机，和 SoC 型号对不上。
// freqs 按 CPU 序号排（和 parts 顺序一致），跟着每组一起缩。
func fitCPUs(parts []fakefs.CPUPart, freqs []int, host int) ([]fakefs.CPUPart, []int) {
	total := 0
	for _, p := range parts {
		total += p.Count
	}
	if total <= host || host <= 0 || len(parts) == 0 {
		return parts, freqs
	}
	counts := make([]int, len(parts))
	left := host
	// 核比组还少：从最后（大核）往前每组给 1 个
	if host < len(parts) {
		for i := len(parts) - 1; i >= 0 && left > 0; i-- {
			counts[i] = 1
			left--
		}
	} else {
		for i := range parts {
			counts[i] = 1
			left--
		}
		// 剩下的按原比例分，零头从大核往前补
		for left > 0 {
			best, bestScore := -1, -1.0
			for i := len(parts) - 1; i >= 0; i-- {
				if counts[i] >= parts[i].Count {
					continue
				}
				score := float64(parts[i].Count) / float64(counts[i]+1)
				if score > bestScore {
					best, bestScore = i, score
				}
			}
			if best < 0 {
				break
			}
			counts[best]++
			left--
		}
	}
	var out []fakefs.CPUPart
	var outFreqs []int
	start := 0
	for i, p := range parts {
		if counts[i] > 0 {
			q := p
			q.Count = counts[i]
			out = append(out, q)
			for j := 0; j < counts[i]; j++ {
				if start+j < len(freqs) {
					outFreqs = append(outFreqs, freqs[start+j])
				}
			}
		}
		start += p.Count
	}
	if len(freqs) == 0 {
		outFreqs = freqs
	}
	return out, outFreqs
}

// applyFakes 等安卓开机完成，再把伪造文件挂进去。在后台跑，结果记在 fakes 里。
func (s *Service) applyFakes(ctx context.Context, id string) {
	name := containerName(id)
	dir := s.fakeDir(id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		s.fakes.set(id, FakeStatus{State: "failed", Error: err.Error()})
		return
	}
	s.fakes.set(id, FakeStatus{State: "pending"})
	deadline := time.Now().Add(s.bootTimeout())
	for {
		out, _ := s.run.Run(ctx, "docker", "exec", name, "getprop", "sys.boot_completed")
		if out == "1" {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			s.fakes.set(id, FakeStatus{State: "failed", Error: "等开机超时"})
			return
		}
		time.Sleep(s.pollEvery())
	}
	targets := map[string]string{}
	for _, f := range s.fakeTargets(id) {
		targets[f.Name] = f.Target
	}
	// 已经挂上的不重复挂（管家重启后补做时，可能有一部分在重启前就挂好了）。
	mounted := mountedTargets(ctx, s.run, name)
	var applied, skipped []string
	for _, e := range entries {
		target, ok := targets[e.Name()]
		if !ok {
			continue
		}
		if mounted[target] {
			applied = append(applied, target)
			continue
		}
		if _, err := s.run.Run(ctx, "docker", "exec", name, "ls", target); err != nil {
			skipped = append(skipped, target+"（容器里没有这个文件）")
			continue
		}
		if out, err := s.run.Run(ctx, "docker", "exec", name, "mount", "-o", "bind", fakeMount+"/"+e.Name(), target); err != nil {
			skipped = append(skipped, fmt.Sprintf("%s（挂载失败：%s）", target, out))
			continue
		}
		applied = append(applied, target)
	}
	// 卸掉临时挂载点并删除目录，安卓根目录里不留痕迹。
	_, _ = s.run.Run(ctx, "docker", "exec", name, "umount", "-l", fakeMount)
	_, _ = s.run.Run(ctx, "docker", "exec", name, "rmdir", fakeMount)
	s.relabelPhone(ctx, id, applied) // 开了真 SELinux 的手机：刚挂上的伪造文件补标签（踩坑 #135）
	s.relabelDataOnce(ctx, id)
	s.fakes.set(id, FakeStatus{State: "applied", Applied: applied, Skipped: skipped})
}

// fakeTargets 读伪造目录里每个文件对应容器里的哪个路径（生成时的映射，开机后要用）。
func (s *Service) fakeTargets(id string) []fakefs.File {
	raw, err := os.ReadFile(filepath.Join(s.fakeDir(id), "..", "fake-targets.json"))
	if err != nil {
		return nil
	}
	var files []fakefs.File
	_ = json.Unmarshal(raw, &files)
	return files
}

func (s *Service) bootTimeout() time.Duration {
	if s.cfg.BootTimeout > 0 {
		return s.cfg.BootTimeout
	}
	return 4 * time.Minute
}

func (s *Service) pollEvery() time.Duration {
	if s.cfg.PollEvery > 0 {
		return s.cfg.PollEvery
	}
	return 3 * time.Second
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func convert(in any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func readHost(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// mountedTargets 容器里当前所有挂载点（读 /proc/mounts 第 2 列）。
func mountedTargets(ctx context.Context, run Runner, name string) map[string]bool {
	out, err := run.Run(ctx, "docker", "exec", name, "cat", "/proc/mounts")
	m := map[string]bool{}
	if err != nil {
		return m
	}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			m[f[1]] = true
		}
	}
	return m
}
