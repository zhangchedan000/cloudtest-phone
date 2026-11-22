package phones

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// 手机开了真 SELinux（踩坑 #135）后，宿主机 docker exec / nsenter 进去跑的命令不在手机自己的 SELinux 命名空间里，
// 安卓的服务管家按手机规则一律拒绝。这类手机开机时由 pp-timens 拉起命令小助手（internal/phonecmd），
// 它在手机的数据目录下开一个套接字（宿主机 <数据目录>/phones/<id>/ppc/s ↔ 手机里 /data/.ppc/s，App 列不了 /data）。
// 这里把管家所有「docker exec <手机> 命令」换成「管家 pp-cmdc <套接字> -- 命令」，业务代码不用改；
// docker cp 进手机的文件随后让小助手 restorecon 一下（宿主机拷进去的文件没有手机认的标签，安卓读不了）。

const cmdMount = "/data/.ppc"

// cmdDir 宿主机上放这台手机命令套接字的目录。
func (s *Service) cmdDir(id string) string { return filepath.Join(s.cfg.DataDir, "phones", id, "ppc") }

// cmdSock 这台手机（容器名）的命令小助手在不在：在就返回套接字路径。
func (s *Service) cmdSock(container string) string {
	if s.cfg.SELinux == "" || s.cfg.InitHelper == "" || !strings.HasPrefix(container, "pp-") {
		return ""
	}
	p := filepath.Join(s.cmdDir(strings.TrimPrefix(container, "pp-")), "s")
	if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
		return p
	}
	return ""
}

// rewriteExec 「docker exec [-e K=V] [-i] <手机> 命令...」→「管家 pp-cmdc <套接字> -e K=V -- 命令...」。不是这类就返回 ok=false。
func (s *Service) rewriteExec(name string, args []string) (string, []string, bool) {
	if name != "docker" || len(args) < 3 || args[0] != "exec" {
		return name, args, false
	}
	var env []string
	i := 1
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		switch args[i] {
		case "-e", "--env", "-u", "--user", "-w", "--workdir":
			if args[i] == "-e" || args[i] == "--env" {
				if i+1 < len(args) {
					env = append(env, args[i+1])
				}
			}
			i += 2
		default:
			i++
		}
	}
	if i >= len(args)-1 {
		return name, args, false
	}
	sock := s.cmdSock(args[i])
	if sock == "" {
		return name, args, false
	}
	out := []string{"pp-cmdc", sock}
	for _, kv := range env {
		out = append(out, "-e", kv)
	}
	out = append(out, "--")
	return s.cfg.InitHelper, append(out, args[i+1:]...), true
}

// cpTarget 「docker cp <源> <手机>:<路径>」拷进手机的路径（不是就返回空）。
func (s *Service) cpTarget(name string, args []string) (container, path string) {
	if name != "docker" || len(args) < 3 || args[0] != "cp" {
		return "", ""
	}
	dst := args[len(args)-1]
	if i := strings.IndexByte(dst, ':'); i > 0 && strings.HasPrefix(dst, "pp-") {
		return dst[:i], dst[i+1:]
	}
	return "", ""
}

// phoneExecRunner 把 Runner 上的 docker exec 换成走命令小助手。
type phoneExecRunner struct {
	inner Runner
	s     *Service
}

func (r phoneExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if n, a, ok := r.s.rewriteExec(name, args); ok {
		return r.inner.Run(ctx, n, a...)
	}
	out, err := r.inner.Run(ctx, name, args...)
	if c, p := r.s.cpTarget(name, args); err == nil && c != "" {
		if sock := r.s.cmdSock(c); sock != "" {
			_, _ = r.inner.Run(ctx, r.s.cfg.InitHelper, "pp-cmdc", sock, "--", "restorecon", "-R", p)
		}
	}
	return out, err
}

type phoneExecRaw struct {
	inner RawRunner
	s     *Service
}

func (r phoneExecRaw) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if n, a, ok := r.s.rewriteExec(name, args); ok {
		return r.inner.Output(ctx, n, a...)
	}
	return r.inner.Output(ctx, name, args...)
}

// cmdClientCommand 流式命令（实时画面的 screenrecord）走命令小助手；断开 / 取消时整组杀。
func (s *Service) cmdClientCommand(ctx context.Context, sock string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, s.cfg.InitHelper, append([]string{"pp-cmdc", sock, "--"}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 3 * time.Second
	return cmd
}

// selinuxBind 开机时：这台手机要开 SELinux 就准备好命令套接字目录，返回要加的挂载（不开返回空）。
func (s *Service) selinuxBind(ctx context.Context, id, image string, entrypoint []string) string {
	if s.cfg.SELinux == "" || entrypoint == nil || s.imageLabel(ctx, image, "prism.selinux") == "" {
		return ""
	}
	d := s.cmdDir(id)
	_ = os.RemoveAll(d)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return ""
	}
	return d + ":" + cmdMount
}
