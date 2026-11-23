package phones

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// 内存（2026-11-16 用户定：每台手机的内存上限按机型来，例如 8GB）：
//   - 容器内存上限 = 机型档案的 memTotal（GB，十进制，和厂商标的一样；8GB ≈ 7629 MiB，真机 MemTotal 也是 7.x GiB）。
//   - 手机里的 /proc/meminfo 用 lxcfs 按容器自己的上限和用量显示（以前显示宿主机 24GB）。
//   - 开机前按「正在运行的手机上限之和 + 这台 ≤ 宿主机内存 - 预留」把关，内存不够就不让开，
//     不会把宿主机挤爆（阿什本 24GB：8GB 的手机同时 2 台）。
// 没装 lxcfs 的机器只改上限、/proc/meminfo 仍是宿主机的；-mem-follow-model=false 退回固定的 -mem-mb。
// 改不了的：sysinfo() 系统调用（部分原生代码用）仍返回宿主机内存，见踩坑 #54。

const (
	lxcfsProc     = "/var/lib/lxcfs/proc"
	lxcfsMount    = "/data/.ppl" // 放 /data 下：App 列不了 /data，卸载后即使 rmdir 没删掉也看不到（2026-11 体检，同 fakeMount）
	hostReserveMB = 3072         // 给宿主机自己（系统、mihomo、管家、Docker）留的
)

// memLimitMB 这台手机的容器内存上限（MiB）。
func (s *Service) memLimitMB(byKey map[string]any) int64 {
	limit := s.cfg.MemoryMB
	if s.cfg.MemFollowModel {
		if gb, ok := toInt(byKey["memTotal"]); ok && gb > 0 && gb <= 64 {
			limit = int64(gb) * 1_000_000_000 / (1 << 20)
		}
	}
	if host := s.hostMB(); host > 0 && limit > host-hostReserveMB {
		limit = host - hostReserveMB
	}
	return limit
}

func (s *Service) hostMB() int64 {
	if s.cfg.HostMemMB > 0 {
		return s.cfg.HostMemMB
	}
	return hostMemMB()
}

// hostMemMB 宿主机总内存（MiB）；读不到返回 0。
func hostMemMB() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			return kb / 1024
		}
	}
	return 0
}

// checkMemoryBudget 开机前把关：正在运行的手机内存上限之和 + 这台，不能超过宿主机内存 - 预留。
// skip：正在开机、已经算进 mine 里的手机（不再按运行中重复算）。
func (s *Service) checkMemoryBudget(ctx context.Context, id string, mine int64, skip map[string]bool) error {
	host := s.hostMB()
	if host <= 0 || !s.cfg.MemFollowModel {
		return nil
	}
	st := s.store.Snapshot()
	used := int64(0)
	for _, other := range st.Phones {
		if other.ID == id || skip[other.ID] || !s.isRunning(ctx, other.ID) {
			continue
		}
		byKey := map[string]any{}
		for _, r := range s.resolve(other, st) {
			byKey[r.Key] = r.Value
		}
		used += s.memLimitMB(byKey)
	}
	factor := s.cfg.MemOvercommit
	if factor < 1 {
		factor = 1
	}
	budget := int64(float64(host-hostReserveMB) * factor)
	if used+mine > budget {
		extra := ""
		if factor > 1 {
			extra = fmt.Sprintf("（已按 %.1f 倍超卖算）", factor)
		}
		return fmt.Errorf("%w：内存不够——正在运行的手机上限加起来 %.1f GB，这台要 %.1f GB，这台机器能给手机用的一共 %.1f GB%s（总 %.1f GB，留 %.1f GB 给系统）。先关掉一台再开",
			ErrBlocked, float64(used)/1024, float64(mine)/1024, float64(budget)/1024, extra, float64(host)/1024, float64(hostReserveMB)/1024)
	}
	// 超卖时上限加起来可以超过物理内存（手机平时只用 1~2GB），所以还要看机器实际还剩多少：
	// 新开一台至少要留出 minFreeForStartMB，不够就不开，免得大家一起被内核杀进程。
	if factor > 1 {
		if avail := availableMB(); avail >= 0 && avail < minFreeForStartMB {
			return fmt.Errorf("%w：内存不够——机器实际只剩 %.1f GB 可用（开一台至少要 %.1f GB）。先关掉一台再开", ErrBlocked, float64(avail)/1024, float64(minFreeForStartMB)/1024)
		}
	}
	return nil
}

// minFreeForStartMB 超卖时新开一台手机前，机器至少还要剩这么多可用内存（开机过程要 1.5GB 左右，再留点余量）。
const minFreeForStartMB = 3072

// availableMB 机器实际可用内存（/proc/meminfo 的 MemAvailable）；读不到返回 -1。
var availableMB = func() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return -1
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "MemAvailable:" {
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			return kb / 1024
		}
	}
	return -1
}

// LxcfsAvailable 宿主机上 lxcfs 在运行吗（有它的 meminfo 文件）。
func LxcfsAvailable() bool {
	_, err := os.Stat(lxcfsProc + "/meminfo")
	return err == nil
}

// applyLxcfs 开机后把 lxcfs 的 meminfo 盖到 /proc/meminfo 上（已经盖过就跳过），然后卸掉临时挂载点。
func (s *Service) applyLxcfs(ctx context.Context, name string) error {
	if _, err := s.run.Run(ctx, "docker", "exec", name, "ls", lxcfsMount+"/meminfo"); err != nil {
		return nil // 这台开机时没挂 lxcfs
	}
	if !mountedTargets(ctx, s.run, name)["/proc/meminfo"] {
		if out, err := s.run.Run(ctx, "docker", "exec", name, "mount", "--bind", lxcfsMount+"/meminfo", "/proc/meminfo"); err != nil {
			return fmt.Errorf("内存信息挂载失败：%v %s", err, out)
		}
	}
	_, _ = s.run.Run(ctx, "docker", "exec", name, "umount", "-l", lxcfsMount)
	_, _ = s.run.Run(ctx, "docker", "exec", name, "rmdir", lxcfsMount)
	return nil
}
