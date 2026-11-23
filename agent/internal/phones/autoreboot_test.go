package phones

import (
	"testing"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/docker"
)

func TestRebootDecision(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339Nano) }
	handled, hist := map[string]string{}, map[string][]time.Time{}
	st := docker.ContainerState{ExitCode: 129, FinishedAt: at(5 * time.Second)}
	if ok, _ := rebootDecision("a", st, handled, hist, now); !ok {
		t.Fatal("安卓自己重启（129）应自动开机")
	}
	if ok, _ := rebootDecision("a", st, handled, hist, now); ok {
		t.Fatal("同一次退出只处理一次")
	}
	for _, c := range []docker.ContainerState{
		{ExitCode: 130, FinishedAt: at(time.Second)},      // 安卓关机
		{ExitCode: 137, FinishedAt: at(time.Second)},      // 管家 docker stop 到点强杀
		{ExitCode: 129, FinishedAt: at(10 * time.Minute)}, // 管家启动前就退出的
		{Running: true, ExitCode: 129, FinishedAt: at(time.Second)},
	} {
		if ok, _ := rebootDecision("b", c, map[string]string{}, map[string][]time.Time{}, now); ok {
			t.Fatalf("不该自动开机：%+v", c)
		}
	}
	// 开不了机反复重启：10 分钟内最多 3 次
	n := 0
	for i := 0; i < 5; i++ {
		c := docker.ContainerState{ExitCode: 129, FinishedAt: at(time.Duration(i) * time.Millisecond)}
		if ok, _ := rebootDecision("c", c, handled, hist, now); ok {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("10 分钟内应只自动开 3 次，实际 %d", n)
	}
}
