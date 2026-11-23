package phones

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/docker"
)

// 手机里的安卓自己重启（设置里点重启、App 或系统触发）：容器里的 init 一调 reboot 就退出——内核对 PID 命名空间里
// reboot(RESTART) 的处理是让 init 以 SIGHUP 结束，Docker 记退出码 129（关机是 SIGINT，130）。以前容器就停在那里，
// 要去管理页手动再开；真手机重启完会自己开回来，这里也一样：发现退出码 129 就自动开机（2026-11-23 用户要求）。
// 管家自己关机（docker stop）不会是 129。开机失败的安卓也会走「重启」（InitFatalReboot），所以 10 分钟内最多自动开 3 次，
// 再多就停下（留给人看）。

const (
	rebootExitCode    = 129
	rebootWindow      = 10 * time.Minute
	rebootMaxInWindow = 3
)

// WatchReboots 管家启动后一直跑：每 5 秒看一遍各台手机的容器。
func (s *Service) WatchReboots(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	handled := map[string]string{}   // 已经处理过的那次退出（按退出时间认）
	hist := map[string][]time.Time{} // 最近自动开机的时间
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, p := range s.store.Snapshot().Phones {
			s.checkReboot(ctx, p.ID, handled, hist)
		}
	}
}

func (s *Service) checkReboot(ctx context.Context, id string, handled map[string]string, hist map[string][]time.Time) {
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	if err != nil {
		return
	}
	start, why := rebootDecision(id, info.State, handled, hist, time.Now())
	if why != "" {
		log.Printf("手机 %s：%s", id, why)
	}
	if !start {
		return
	}
	go func() {
		if err := s.Start(context.Background(), id); err != nil {
			log.Printf("手机 %s：重启后自动开机失败：%v", id, err)
		}
	}()
}

// rebootDecision 这次容器状态要不要自动开机（why 非空时记日志）。
func rebootDecision(id string, st docker.ContainerState, handled map[string]string, hist map[string][]time.Time, now time.Time) (bool, string) {
	if st.Running || st.ExitCode != rebootExitCode || st.FinishedAt == "" || handled[id] == st.FinishedAt {
		return false, ""
	}
	handled[id] = st.FinishedAt
	// 管家启动前就退出了的不管（不知道是不是有人故意留着）
	if ft, err := time.Parse(time.RFC3339Nano, st.FinishedAt); err != nil || now.Sub(ft) > 2*time.Minute {
		return false, ""
	}
	var recent []time.Time
	for _, t := range hist[id] {
		if now.Sub(t) < rebootWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= rebootMaxInWindow {
		hist[id] = recent
		return false, fmt.Sprintf("安卓 %v 内自己重启了 %d 次以上，不再自动开机（可能开不了机，去管理页看看）", rebootWindow, rebootMaxInWindow)
	}
	hist[id] = append(recent, now)
	return true, "安卓自己重启了，自动开回来"
}
