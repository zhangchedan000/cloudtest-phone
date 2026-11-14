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
// 伪造文件先放在容器里的临时挂载点 fakeMount，挂完就卸掉并删除目录，不留在安卓根目录里。
//
// 已知残留：容器里的 /proc/self/mountinfo 能看到这些 bind mount（LXCFS 也有同样的问题）。
// 彻底解决要改内核（见 docs/进度记录.md 待办）。

const fakeMount = "/.ppfake"

// Runner 执行宿主机命令（测试时换成假的）。
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
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

func fitCPUs(parts []fakefs.CPUPart, freqs []int, host int) ([]fakefs.CPUPart, []int) {
	total := 0
	for _, p := range parts {
		total += p.Count
	}
	if total <= host || host <= 0 {
		return parts, freqs
	}
	var out []fakefs.CPUPart
	left := host
	for _, p := range parts {
		if left == 0 {
			break
		}
		if p.Count > left {
			p.Count = left
		}
		left -= p.Count
		out = append(out, p)
	}
	if len(freqs) > host {
		freqs = freqs[:host]
	}
	return out, freqs
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
	var applied, skipped []string
	for _, e := range entries {
		target, ok := targets[e.Name()]
		if !ok {
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
