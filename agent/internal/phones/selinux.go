package phones

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/phonecmd"
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

// relabelPhone 开了真 SELinux 的手机：让命令小助手重新给挂进来的东西补标签（管家开机后才挂的伪造文件，extra 是它们在手机里的路径）。
func (s *Service) relabelPhone(ctx context.Context, id string, extra []string) {
	sock := s.cmdSock(containerName(id))
	if sock == "" {
		return
	}
	s.addBindPaths(id, extra)
	_, _ = s.run.Run(ctx, s.cfg.InitHelper, "pp-cmdc", sock, "--", phonecmd.RelabelCmd)
}

// addBindPaths 往「挂进手机的路径」清单里加（小助手补标签用，见 phonecmd.BindsFile）。
func (s *Service) addBindPaths(id string, paths []string) {
	if len(paths) == 0 {
		return
	}
	f, err := os.OpenFile(filepath.Join(s.cmdDir(id), "binds"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	for _, p := range paths {
		_, _ = f.WriteString(p + "\n")
	}
}

// relabelDataOnce 开了真 SELinux 的手机第一次开机：/data 里以前（没开 SELinux 时）建的文件都没有标签或标签不对，
// 让小助手用安卓自己的 restorecon 整个重打一遍（-D：App 数据目录 /data/data、/data/user 也打，按 seapp_contexts，
// 带 App 自己的分类；不加 -D 安卓会跳过这些目录）。只做一次（/data 里留记号）。
func (s *Service) relabelDataOnce(ctx context.Context, id string) {
	sock := s.cmdSock(containerName(id))
	if sock == "" {
		return
	}
	if _, err := os.Stat(s.seFreshMark(id)); err == nil {
		return
	}
	const mark = "/data/system/.pp_se_relabeled2"
	if _, err := s.run.Run(ctx, s.cfg.InitHelper, "pp-cmdc", sock, "--", "test", "-f", mark); err == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if out, err := s.run.Run(cctx, s.cfg.InitHelper, "pp-cmdc", sock, "--", "restorecon", "-R", "-F", "-D", "/data"); err != nil {
		log.Printf("手机 %s：/data 重打 SELinux 标签失败：%v %s", id, err, out)
		return
	}
	_, _ = s.run.Run(ctx, s.cfg.InitHelper, "pp-cmdc", sock, "--", "touch", mark)
	log.Printf("手机 %s：/data 按 SELinux 规则重打了标签（第一次开 SELinux）", id)
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
	// 新手机（/data 里还没有安卓建的 system 目录）：数据从第一次开机起就是在 SELinux 下建的，标签本来就对，
	// 不用开机后再整个 restorecon 一遍（几台一起开机时白白加负载）
	if _, err := os.Stat(filepath.Join(s.dataDir(id), "system")); os.IsNotExist(err) {
		_ = os.WriteFile(s.seFreshMark(id), nil, 0o600)
	}
	return d + ":" + cmdMount
}

// seFreshMark 宿主机上的记号：这台手机的 /data 从一开始就是开着 SELinux 建的（relabelDataOnce 跳过）。
func (s *Service) seFreshMark(id string) string {
	return filepath.Join(s.phoneDir(id), "se-fresh-data")
}

// writeBindPaths 开机前：把这台手机所有 bind 挂载在手机里的路径写给小助手（它开机前照这个补标签）。
func (s *Service) writeBindPaths(id string, binds []string) {
	var ps []string
	for _, b := range binds {
		f := strings.Split(b, ":")
		if len(f) >= 2 && strings.HasPrefix(f[1], "/") {
			ps = append(ps, f[1])
		}
	}
	s.addBindPaths(id, ps)
}
