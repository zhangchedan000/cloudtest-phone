package phones

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// 开了时间命名空间（踩坑 #49）后，docker exec 进容器的进程还在宿主机的时间里（runc 不知道时间命名空间），
// 和安卓自己的进程时钟差了宿主机的开机时长。大多数命令无所谓，但 screenrecord 要和 SurfaceFlinger /
// 编码器对时间戳：新开的手机上 docker exec screenrecord 一帧都出不来（实测 0 字节，进时间命名空间跑 1.5MB，踩坑 #50）。
// 这类对时间敏感的命令改用宿主机的 nsenter 进容器全部命名空间（含时间）来跑。

// androidPath 安卓 shell 的 PATH（nsenter 不像 docker exec 那样带容器的环境变量）。
const androidPath = "PATH=/product/bin:/apex/com.android.runtime/bin:/apex/com.android.art/bin:/system_ext/bin:/system/bin:/system/xbin:/odm/bin:/vendor/bin:/vendor/xbin"

// phoneCommand 在手机里跑命令：开了时间命名空间就用 nsenter（含时间命名空间），否则 docker exec。
func (s *Service) phoneCommand(ctx context.Context, id string, args ...string) *exec.Cmd {
	if sock := s.cmdSock(containerName(id)); sock != "" {
		return s.cmdClientCommand(ctx, sock, args) // 开了真 SELinux 的手机（踩坑 #135）
	}
	if s.cfg.InitHelper != "" {
		if info, err := s.docker.InspectContainer(ctx, containerName(id)); err == nil && info.State.Pid > 0 {
			if _, err := exec.LookPath("nsenter"); err == nil {
				full := append([]string{"-t", fmt.Sprint(info.State.Pid), "-a", "--", "/system/bin/env", "-i", androidPath, "ANDROID_DATA=/data", "ANDROID_ROOT=/system"}, args...)
				cmd := exec.CommandContext(ctx, "nsenter", full...)
				// nsenter 进 PID 命名空间时会 fork，真正干活的（比如 screenrecord）是它的子进程：只杀 nsenter 子进程不会停，
				// 输出管道也一直开着（页面关了 screenrecord 还在跑）。所以放进单独的进程组，取消时整组杀。
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
				cmd.WaitDelay = 3 * time.Second
				return cmd
			}
		}
	}
	return exec.CommandContext(ctx, "docker", append([]string{"exec", containerName(id)}, args...)...)
}
