package phones

import (
	"context"
	"fmt"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/touch"
)

// 摇一摇（2026-11-27 用户要求，对齐魔云腾）：让加速度计抖一下，App 的摇一摇功能就触发。
// 走已有命令通道 touch.Send（和点击、收短信一个路子），pp-init 里给当前传感器 Feeder 灌一段抖动。

// Shake 摇一摇这台手机。
func (s *Service) Shake(ctx context.Context, id string) error {
	if err := s.requireRunning(ctx, id, true); err != nil {
		return err
	}
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	if err != nil || info.State.Pid <= 0 {
		return fmt.Errorf("拿不到容器进程：%v", err)
	}
	ns := fmt.Sprintf("/proc/%d/ns/net", info.State.Pid)
	if err := touch.Send(ns, "shake", 3*time.Second); err != nil {
		return fmt.Errorf("摇一摇失败：%w", err)
	}
	return nil
}
