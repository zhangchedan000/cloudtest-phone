// Package hostguard 守住宿主机的内核设置（踩坑 #58）。
//
// 云手机容器是特权容器，安卓开机时 init.rc 里的 `write /proc/sys/...` 会直接改宿主机的全局内核设置
// （net.* 是每台手机自己的网络命名空间，不受影响；kernel.* / vm.* / abi.* 是整台机器共用的）。实测被改掉的有：
//   - kernel.modprobe 被清空 → 宿主机再也不能自动加载内核模块（WiFi 的 ccm 加载不了就是它）；
//   - kernel.panic_on_oops=1 → 内核一出错整台机器就死机（kernel.panic=0 时不会自己重启）= 失联；
//   - vm.watermark_scale_factor=1795（extra_free_kbytes.sh 按屏幕算的）→ 宿主机白白空着约 7GB 内存；
//   - vm.overcommit_memory=1、kernel.perf_event_paranoid=-1（降低宿主机安全）、hung_task_timeout_secs=0 等。
//
// 安卓的 init 会在自己的 /proc 上重新挂一个 proc，开机前把 /proc/sys 设成只读挡不住，
// 所以改成「守」：管家启动时、每台手机开机后、以后每 30 秒，发现被改了就改回来（并写进 /etc/sysctl.d，宿主机重启后也对）。
package hostguard

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Wanted 宿主机应有的值（Ubuntu / 内核默认值；kernel.panic=10 是为了万一死机 10 秒后自动重启，不失联）。
var Wanted = map[string]string{
	"kernel.modprobe":               "/sbin/modprobe",
	"kernel.panic_on_oops":          "0",
	"kernel.panic":                  "10",
	"kernel.hung_task_timeout_secs": "120",
	"kernel.perf_event_paranoid":    "4",
	"kernel.kptr_restrict":          "1",
	"kernel.sysrq":                  "176",
	"kernel.pid_max":                "4194304",
	"kernel.randomize_va_space":     "2",
	"vm.overcommit_memory":          "0",
	"vm.watermark_scale_factor":     "10",
	"vm.watermark_boost_factor":     "15000",
	"vm.dirty_background_ratio":     "10",
	"vm.dirty_expire_centisecs":     "3000",
	"vm.mmap_min_addr":              "32768",
}

// Root /proc/sys（测试时换成临时目录）。
var Root = "/proc/sys"

// ConfFile 写给宿主机开机用的 sysctl 配置。
var ConfFile = "/etc/sysctl.d/90-prism-phone-host.conf"

var mu sync.Mutex

func path(key string) string { return filepath.Join(Root, strings.ReplaceAll(key, ".", "/")) }

// Fix 检查一遍，被改掉的改回来。返回改了哪些（"键 旧值→新值"）。内核没有的键跳过。
func Fix() []string {
	mu.Lock()
	defer mu.Unlock()
	keys := make([]string, 0, len(Wanted))
	for k := range Wanted {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var fixed []string
	for _, k := range keys {
		p := path(k)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		cur := strings.TrimSpace(string(b))
		if cur == Wanted[k] {
			continue
		}
		if err := os.WriteFile(p, []byte(Wanted[k]+"\n"), 0); err != nil {
			log.Printf("宿主机内核设置 %s=%q 改不回 %s：%v", k, cur, Wanted[k], err)
			continue
		}
		fixed = append(fixed, fmt.Sprintf("%s %q→%s", k, cur, Wanted[k]))
	}
	if len(fixed) > 0 {
		log.Printf("宿主机内核设置被手机改过，已改回：%s", strings.Join(fixed, "，"))
	}
	return fixed
}

// WriteConf 写 /etc/sysctl.d 配置（内容没变就不写）。
func WriteConf() error {
	keys := make([]string, 0, len(Wanted))
	for k := range Wanted {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# prism-phone 管家程序写的：宿主机内核设置，防止被云手机（安卓 init.rc）改掉（踩坑 #58）。手动改了会被管家改回。\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, Wanted[k])
	}
	if old, err := os.ReadFile(ConfFile); err == nil && string(old) == b.String() {
		return nil
	}
	return os.WriteFile(ConfFile, []byte(b.String()), 0o644)
}
