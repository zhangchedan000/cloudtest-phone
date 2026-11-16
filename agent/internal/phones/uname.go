package phones

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
)

// 手机里 uname() 的内核版本（踩坑 #64）：宿主机装了 pp_uts 内核模块（host/build-kmod.sh）就有 /proc/pp_uts，
// 容器一启动就把这台的 kernelRelease / kernelBuild 写进它自己的 UTS 命名空间（只改容器那份，宿主机不受影响）。
// 没有模块就算了（uname 照旧是宿主机内核，/proc/version 等文件还是 fakefs 伪造的）。

// UnameCtl 模块的控制文件（测试时换掉）。
var UnameCtl = "/proc/pp_uts"

func (s *Service) applyUname(ctx context.Context, id string, byKey map[string]any) {
	rel := strings.TrimSpace(str(byKey["kernelRelease"]))
	if rel == "" {
		return
	}
	if _, err := os.Stat(UnameCtl); err != nil {
		return
	}
	ver := strings.TrimSpace(str(byKey["kernelBuild"]))
	if ver == "" {
		ver = "#1 SMP PREEMPT"
	}
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	if err != nil || info.State.Pid <= 0 {
		return
	}
	line := fmt.Sprintf("%d|%s|%s", info.State.Pid, clip(rel, 64), clip(ver, 64))
	if err := os.WriteFile(UnameCtl, []byte(line), 0); err != nil {
		log.Printf("手机 %s：设内核版本号失败：%v", id, err)
	}
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
