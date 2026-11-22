package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/zhangchedan000/prism-phone/agent/internal/phonecmd"
)

// runCmdServer 手机里的命令小助手（不返回，见 phonecmd 包）。开机前先补标签，补完往 fd 3 写一个字节
// 告诉 pp-timens 可以启动安卓了。
func runCmdServer(sock string) {
	_ = os.WriteFile("/proc/self/comm", []byte("vendor.cmdd"), 0)
	n, err := phonecmd.LabelBoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vendor.cmdd：补标签失败：%v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "vendor.cmdd：补了 %d 个标签\n", n)
	}
	if f := os.NewFile(3, "ready"); f != nil {
		_, _ = f.Write([]byte{1})
		_ = f.Close()
	}
	err = phonecmd.Serve(sock)
	fmt.Fprintf(os.Stderr, "vendor.cmdd：%v\n", err)
	os.Exit(1)
}

// runCmdClient args = <套接字> [-e K=V]... [--] 命令...
func runCmdClient(args []string) {
	sock := args[0]
	args = args[1:]
	var env []string
	for len(args) > 0 {
		if args[0] == "-e" && len(args) > 1 {
			env = append(env, args[1])
			args = args[2:]
			continue
		}
		if strings.HasPrefix(args[0], "-e") && len(args[0]) > 2 {
			env = append(env, args[0][2:])
			args = args[1:]
			continue
		}
		if args[0] == "--" {
			args = args[1:]
		}
		break
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法：pp-cmdc <套接字> [-e K=V]... -- 命令...")
		os.Exit(2)
	}
	code, err := phonecmd.Run(sock, env, args, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pp-cmdc：%v\n", err)
	}
	os.Exit(code)
}
