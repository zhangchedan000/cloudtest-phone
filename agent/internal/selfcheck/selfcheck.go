// Package selfcheck 环境自检：启动时和 /api/health 都会跑，缺什么明确说怎么补（原则 5）。
package selfcheck

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type Item struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Info string `json:"info"`
	Fix  string `json:"fix,omitempty"`
}

type System struct {
	Arch     string  `json:"arch"`
	Kernel   string  `json:"kernel"`
	CPUs     int     `json:"cpus"`
	MemGB    float64 `json:"memGb"`
	MemAvail float64 `json:"memAvailGb"`
	DiskGB   float64 `json:"diskGb"`
	DiskFree float64 `json:"diskFreeGb"`
}

type Report struct {
	OK     bool   `json:"ok"`
	Items  []Item `json:"items"`
	System System `json:"system"`
}

// Run 检查宿主机。ping 用来检查 Docker 是否可用。
func Run(dataDir string, ping func(context.Context) error) Report {
	var items []Item
	add := func(name string, ok bool, info, fix string) { items = append(items, Item{name, ok, info, fix}) }

	add("架构", runtime.GOARCH == "arm64", runtime.GOARCH, "只支持 ARM64 机器")
	page := os.Getpagesize()
	add("内存页 4K", page == 4096, fmt.Sprint(page), "换用 4K 页的内核/发行版（例如 Ubuntu；Oracle Linux 的 ARM 内核是 64K）")
	fsData, _ := os.ReadFile("/proc/filesystems")
	add("binderfs", strings.Contains(string(fsData), "binder"), "", "执行 modprobe binder_linux（host/setup-host.sh 会做）")
	_, psiErr := os.Stat("/proc/pressure/memory")
	add("PSI", psiErr == nil, "", "内核参数加 psi=1，否则安卓 lmkd 会反复重启")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dockerErr := ping(ctx)
	add("Docker", dockerErr == nil, errText(dockerErr), "安装并启动 docker（host/setup-host.sh）")

	sys := System{Arch: runtime.GOARCH, CPUs: runtime.NumCPU()}
	if v, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		sys.Kernel = strings.TrimSpace(string(v))
	}
	sys.MemGB, sys.MemAvail = meminfo()
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dataDir, &fs); err == nil {
		sys.DiskGB = round(float64(fs.Blocks) * float64(fs.Bsize) / (1 << 30))
		sys.DiskFree = round(float64(fs.Bavail) * float64(fs.Bsize) / (1 << 30))
		add("硬盘剩余", sys.DiskFree >= 10, fmt.Sprintf("%.1f GB", sys.DiskFree), "剩余空间少于 10GB，清理旧数据（DataDir/trash）")
	}

	ok := true
	for _, it := range items {
		ok = ok && it.OK
	}
	return Report{OK: ok, Items: items, System: sys}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func meminfo() (total, avail float64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var key string
		var kb float64
		if _, err := fmt.Sscanf(sc.Text(), "%s %f", &key, &kb); err != nil {
			continue
		}
		switch key {
		case "MemTotal:":
			total = round(kb / (1 << 20))
		case "MemAvailable:":
			avail = round(kb / (1 << 20))
		}
	}
	return
}

func round(v float64) float64 { return float64(int(v*10+0.5)) / 10 }
