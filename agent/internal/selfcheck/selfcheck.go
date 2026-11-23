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

// kmodDir 板子上自己编的内核模块放这里（install.sh）。
var kmodDir = "/opt/prism-phone/kmod"

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
	// binder：两条路都算通。
	//   1. binderfs（甲骨文等标准内核）：/proc/filesystems 里有 binder，容器内自动挂 binderfs。
	//   2. 静态 binder 设备（RK3588/魔云腾 Q1 这类内核没 binderfs，只有 CONFIG_ANDROID_BINDER_DEVICES）：
	//      宿主 mknod 出 /dev/binder 指向静态 binderN（host/setup-board.sh 的 binder-nodes）。
	// 只要有一条，安卓就能拿到 binder。只认 binderfs 会把 Q1 这种好板子误判成不可用。
	fsData, _ := os.ReadFile("/proc/filesystems")
	hasBinderfs := strings.Contains(string(fsData), "binder")
	_, binderDevErr := os.Stat("/dev/binder")
	hasBinderDev := binderDevErr == nil
	binderInfo := "无"
	switch {
	case hasBinderfs:
		binderInfo = "binderfs"
	case hasBinderDev:
		binderInfo = "静态 binder 设备"
	}
	add("binder", hasBinderfs || hasBinderDev, binderInfo, "有 binderfs 的内核执行 modprobe binder_linux（host/setup-host.sh）；没 binderfs 的板子（如 RK3588/Q1）用静态 binder 设备 + mknod /dev/binder（host/setup-board.sh 的 binder-nodes）")
	_, psiErr := os.Stat("/proc/pressure/memory")
	add("PSI", psiErr == nil, "", "内核参数加 psi=1，否则安卓 lmkd 会反复重启")
	// 板子上自己编的内核模块（install.sh 装到 /opt/prism-phone/kmod，踩坑 #129 / #134 / #136 / #137 / #140）：
	// pp_mnt 管着两道保护（容器里的安卓写 sysrq、停用宿主机的盘），没加载时手机一重启可能把板子搞瘫。没装过的机器不查。
	if _, err := os.Stat(kmodDir); err == nil {
		var missing []string
		for _, m := range []string{"pp_mnt", "pp_uts", "wlan"} {
			if _, err := os.Stat("/sys/module/" + m); err != nil {
				missing = append(missing, m)
			}
		}
		info := "pp_mnt / pp_uts / wlan"
		if len(missing) > 0 {
			info = "没加载：" + strings.Join(missing, " ")
		}
		add("内核模块", len(missing) == 0, info, "内核换过的话 install.sh 会在后台现编（看 /mmc/pp-build/kmod-auto.log）；编好前别开手机")
	}
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
