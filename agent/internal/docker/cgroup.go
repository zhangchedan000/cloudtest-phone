package docker

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// CgroupRoot cgroup v2 挂载点（测试时换成临时目录）。
var CgroupRoot = "/sys/fs/cgroup"

// cgroupDir 找容器的 cgroup 目录（systemd 驱动 / cgroupfs 驱动两种布局）。
func cgroupDir(root, id string) (string, error) {
	for _, d := range []string{
		filepath.Join(root, "system.slice", "docker-"+id+".scope"),
		filepath.Join(root, "docker", id),
	} {
		if _, err := os.Stat(filepath.Join(d, "memory.current")); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("找不到容器 %s 的 cgroup", id)
}

// CgroupUsage 直接读 cgroup v2 算资源占用（算法同 docker stats：内存扣掉 inactive_file，CPU 采样 1 秒）。
func CgroupUsage(ctx context.Context, root, id string) (Usage, error) {
	dir, err := cgroupDir(root, id)
	if err != nil {
		return Usage{}, err
	}
	cpu0, err := cgroupCPUUsec(dir)
	if err != nil {
		return Usage{}, err
	}
	t0 := time.Now()
	select {
	case <-ctx.Done():
		return Usage{}, ctx.Err()
	case <-time.After(time.Second):
	}
	cpu1, err := cgroupCPUUsec(dir)
	if err != nil {
		return Usage{}, err
	}
	var u Usage
	if el := time.Since(t0).Microseconds(); el > 0 && cpu1 > cpu0 {
		u.CPUPercent = float64(cpu1-cpu0) / float64(el) / float64(runtime.NumCPU()) * 100
	}
	cur, err := readUint(filepath.Join(dir, "memory.current"))
	if err != nil {
		return Usage{}, err
	}
	if st, err := readKV(filepath.Join(dir, "memory.stat")); err == nil {
		if c := st["inactive_file"]; c < cur {
			cur -= c
		}
	}
	u.MemMB = float64(cur) / (1 << 20)
	limit, err := readUint(filepath.Join(dir, "memory.max"))
	if err != nil { // "max" = 不限，和 Docker 一样报整机内存
		limit = hostMemBytes()
	}
	u.MemLimitMB = float64(limit) / (1 << 20)
	return u, nil
}

func cgroupCPUUsec(dir string) (uint64, error) {
	kv, err := readKV(filepath.Join(dir, "cpu.stat"))
	if err != nil {
		return 0, err
	}
	v, ok := kv["usage_usec"]
	if !ok {
		return 0, fmt.Errorf("cpu.stat 没有 usage_usec")
	}
	return v, nil
}

func readUint(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}

func readKV(path string) (map[string]uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil {
			m[k] = n
		}
	}
	return m, sc.Err()
}

func hostMemBytes() uint64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "MemTotal:" {
			n, _ := strconv.ParseUint(f[1], 10, 64)
			return n * 1024
		}
	}
	return 0
}
