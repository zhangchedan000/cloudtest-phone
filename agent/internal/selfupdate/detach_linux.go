package selfupdate

import "syscall"

// detach 让换新脚本脱离管家进程：自立门户（Setsid），这样重启杀掉管家也不会连带杀掉它。
func detach() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
