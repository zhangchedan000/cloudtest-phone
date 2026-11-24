// Package phonecmd 手机里的命令小助手（踩坑 #135）。
//
// 手机开了真 SELinux 以后，管家从宿主机 docker exec / nsenter 进去跑的命令（settings、am、input、screenrecord…）
// 不在手机自己的 SELinux 命名空间里，安卓的服务管家拿到的调用方身份是宿主机的「kernel」，按手机的规则一律拒绝。
// 所以开机时由 pp-timens 在加载完手机规则之后、启动安卓之前拉起这个小助手（它就在手机的 SELinux 命名空间里，
// 身份 u:r:kernel:s0，规则里设成只记录不拦截），管家要跑的命令都交给它在手机里起。
//
// 协议（Unix 套接字，一个连接跑一条命令）：客户端先发一行 JSON {"args":[...],"env":[...]}，之后
// 客户端 → 小助手：'I' 帧 = 标准输入数据，'E' 帧 = 标准输入结束；
// 小助手 → 客户端：'O' 帧 = 标准输出，'R' 帧 = 标准错误，'X' 帧 = 退出码（4 字节）。
// 帧 = 1 字节类型 + 4 字节长度（大端）+ 数据。
package phonecmd

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

// RelabelCmd 小助手的内置命令：重新给挂载进来的东西补标签。
const RelabelCmd = "__pp_relabel"

// LabelTreeCmd 小助手的内置命令：__pp_labeltree <只读挂载点> <目录> <标签>，把目录里所有东西打成同一个标签。
const LabelTreeCmd = "__pp_labeltree"

type request struct {
	Args []string `json:"args"`
	Env  []string `json:"env,omitempty"`
}

// defaultEnv 读不到 init 的环境变量时用的。
var defaultEnv = []string{
	"PATH=/product/bin:/apex/com.android.runtime/bin:/apex/com.android.art/bin:/system_ext/bin:/system/bin:/system/xbin:/odm/bin:/vendor/bin:/vendor/xbin",
	"ANDROID_DATA=/data", "ANDROID_ROOT=/system",
}

// initEnv 安卓 init（容器里的 1 号进程）的环境变量：BOOTCLASSPATH 这些 am / pm 要用，init 开机时才导出。
func initEnv() []string {
	b, err := os.ReadFile("/proc/1/environ")
	if err != nil || len(b) == 0 {
		return defaultEnv
	}
	var env []string
	for _, kv := range strings.Split(string(b), "\x00") {
		if kv != "" {
			env = append(env, kv)
		}
	}
	if len(env) == 0 {
		return defaultEnv
	}
	return env
}

func writeFrame(w io.Writer, mu *sync.Mutex, t byte, data []byte) error {
	mu.Lock()
	defer mu.Unlock()
	var h [5]byte
	h[0] = t
	binary.BigEndian.PutUint32(h[1:], uint32(len(data)))
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var h [5]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(h[1:])
	if n > 16<<20 {
		return 0, nil, fmt.Errorf("帧太大：%d", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return 0, nil, err
	}
	return h[0], data, nil
}

type frameWriter struct {
	w  io.Writer
	mu *sync.Mutex
	t  byte
}

func (f frameWriter) Write(p []byte) (int, error) {
	if err := writeFrame(f.w, f.mu, f.t, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Serve 小助手（不返回）：在 sock 上等管家的命令。
func Serve(sock string) error {
	_ = os.MkdirAll(dirOf(sock), 0o700)
	_ = os.Remove(sock)
	syscall.Umask(0o077)
	l, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	for {
		c, err := l.Accept()
		if err != nil {
			continue
		}
		go handle(c)
	}
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "."
}

func handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return
	}
	var req request
	var mu sync.Mutex
	if json.Unmarshal(line, &req) != nil || len(req.Args) == 0 {
		_ = writeFrame(c, &mu, 'R', []byte("phonecmd: 请求不对\n"))
		_ = writeFrame(c, &mu, 'X', []byte{0, 0, 0, 127})
		return
	}
	// 内置命令：给中转目录里的整棵伪造目录打标签（挂到 /sys/devices/system/cpu 之前，踩坑 #147）
	if req.Args[0] == LabelTreeCmd && len(req.Args) == 4 {
		n, err := LabelTree(req.Args[1], req.Args[2], req.Args[3])
		msg, code := fmt.Sprintf("打了 %d 个标签\n", n), byte(0)
		if err != nil {
			msg, code = err.Error()+"\n", 1
		}
		_ = writeFrame(c, &mu, 'O', []byte(msg))
		_ = writeFrame(c, &mu, 'X', []byte{0, 0, 0, code})
		return
	}
	// 内置命令：管家挂完伪造文件后让小助手再补一遍标签
	if req.Args[0] == RelabelCmd {
		n, err := LabelBinds()
		msg := fmt.Sprintf("补了 %d 个标签\n", n)
		code := byte(0)
		if err != nil {
			msg, code = err.Error()+"\n", 1
		}
		_ = writeFrame(c, &mu, 'O', []byte(msg))
		_ = writeFrame(c, &mu, 'X', []byte{0, 0, 0, code})
		return
	}
	cmd := exec.Command(req.Args[0], req.Args[1:]...)
	env := initEnv()
	cmd.Env = append(env, req.Env...)
	cmd.Dir = "/"
	cmd.Stdout = frameWriter{c, &mu, 'O'}
	cmd.Stderr = frameWriter{c, &mu, 'R'}
	stdin, _ := cmd.StdinPipe()
	if p, err := exec.LookPath(req.Args[0]); err == nil {
		cmd.Path = p
	} else if !strings.Contains(req.Args[0], "/") {
		// exec.LookPath 用的是小助手自己的 PATH：按安卓的 PATH 再找一次
		for _, kv := range env {
			if strings.HasPrefix(kv, "PATH=") {
				for _, d := range strings.Split(kv[5:], ":") {
					if st, err := os.Stat(d + "/" + req.Args[0]); err == nil && !st.IsDir() {
						cmd.Path = d + "/" + req.Args[0]
						break
					}
				}
			}
		}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = writeFrame(c, &mu, 'R', []byte(err.Error()+"\n"))
		_ = writeFrame(c, &mu, 'X', []byte{0, 0, 0, 127})
		return
	}
	var exited atomic.Bool
	go func() { // 标准输入；连接断了（管家取消）就整组杀掉
		for {
			t, data, err := readFrame(br)
			if err != nil {
				if !exited.Load() {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				}
				return
			}
			switch t {
			case 'I':
				_, _ = stdin.Write(data)
			case 'E':
				_ = stdin.Close()
			}
		}
	}()
	err = cmd.Wait()
	exited.Store(true)
	code := 0
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		code = ee.ExitCode()
		if code < 0 {
			code = 128 + 9
		}
	} else if err != nil {
		code = 127
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(code))
	_ = writeFrame(c, &mu, 'X', b[:])
}

// Run 客户端：在手机里跑 args，标准输入 / 输出 / 错误接到给的流上，返回退出码。
func Run(sock string, env, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	c, err := net.Dial("unix", sock)
	if err != nil {
		return 127, err
	}
	defer c.Close()
	req, _ := json.Marshal(request{Args: args, Env: env})
	if _, err := c.Write(append(req, '\n')); err != nil {
		return 127, err
	}
	var mu sync.Mutex
	if stdin != nil {
		go func() {
			buf := make([]byte, 32<<10)
			for {
				n, err := stdin.Read(buf)
				if n > 0 && writeFrame(c, &mu, 'I', buf[:n]) != nil {
					return
				}
				if err != nil {
					_ = writeFrame(c, &mu, 'E', nil)
					return
				}
			}
		}()
	} else {
		_ = writeFrame(c, &mu, 'E', nil)
	}
	for {
		t, data, err := readFrame(c)
		if err != nil {
			return 127, fmt.Errorf("手机里的命令小助手断了：%w", err)
		}
		switch t {
		case 'O':
			if _, err := stdout.Write(data); err != nil {
				return 141, err
			}
		case 'R':
			_, _ = stderr.Write(data)
		case 'X':
			if len(data) == 4 {
				return int(binary.BigEndian.Uint32(data)), nil
			}
			return 127, nil
		}
	}
}
