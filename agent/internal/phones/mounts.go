package phones

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
)

// 手机里读到的挂载表去掉容器痕迹（踩坑 #68）：宿主机装了 pp_mnt 内核模块（host/build-kmod.sh）就有 /proc/pp_mnt。
// 容器一启动就把这台登记上：告诉模块这台的 /data 来自哪块盘（/dev/sda1 或磁盘镜像的 /dev/loopN）、在那块盘上的路径，
// 模块据此把 /data 改成真机的样子，并删掉别的宿主机 bind（build.prop、Docker 的 resolv.conf 等）。
// 没有模块就算了（挂载表照旧）。

// MountsCtl 模块的控制文件（测试时换掉）。
var MountsCtl = "/proc/pp_mnt"

// dataMount 从 mountinfo 里找 /data 那一行：返回它在那块盘上的路径（root）和来源设备。
func dataMount(mountinfo string) (root, src string, ok bool) {
	for _, line := range strings.Split(mountinfo, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[4] != "/data" {
			continue
		}
		for i := 6; i+2 < len(f); i++ {
			if f[i] == "-" {
				root, src = f[3], f[i+2]
			}
		}
	}
	return root, src, root != "" && strings.HasPrefix(src, "/dev/")
}

func (s *Service) applyMounts(ctx context.Context, id string) {
	if _, err := os.Stat(MountsCtl); err != nil {
		return
	}
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	if err != nil || info.State.Pid <= 0 {
		return
	}
	mi, err := os.ReadFile(fmt.Sprintf("/proc/%d/mountinfo", info.State.Pid))
	if err != nil {
		return
	}
	root, src, ok := dataMount(string(mi))
	if !ok || len(root) >= 256 || len(src) >= 64 {
		log.Printf("手机 %s：挂载表里没找到 /data（不改挂载表）", id)
		return
	}
	line := fmt.Sprintf("add %d|%s|%s", info.State.Pid, root, src)
	if err := os.WriteFile(MountsCtl, []byte(line), 0); err != nil {
		log.Printf("手机 %s：登记挂载表改写失败：%v", id, err)
	}
}
