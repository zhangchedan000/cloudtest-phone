package phones

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

// ADB 授权（2026-11-16）：开机参数 ro.debuggable=0（真机出厂值）会让 adbd 强制校验电脑的公钥，
// 没授权的电脑 adb connect 后是 unauthorized。真机是在手机上点「允许 USB 调试」，云手机改成：
// 用户把自己电脑的 ~/.android/adbkey.pub 粘贴到管理页，管家写进手机的 /data/misc/adb/adb_keys。
// /data 在容器外面，开关机都在；重置手机（清数据）后要重新授权。

const adbKeysPath = "/data/misc/adb/adb_keys"

// adbKeyPattern adbkey.pub 的格式：一长串 base64（RSA 公钥，解出来 524 字节）+ 可选的空格和「用户名@电脑名」。
var adbKeyPattern = regexp.MustCompile(`^([A-Za-z0-9+/]+={0,2})(?: ([A-Za-z0-9._@-]{1,64}))?$`)

// checkADBKey 校验并整理公钥（去掉首尾空白和换行）。
func checkADBKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	m := adbKeyPattern.FindStringSubmatch(key)
	if m == nil {
		return "", fmt.Errorf("%w：不是 adbkey.pub 的内容（应该是一长串字母数字，后面可能跟着 用户名@电脑名）", ErrInvalid)
	}
	raw, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil || len(raw) != 524 {
		return "", fmt.Errorf("%w：公钥长度不对，请复制 adbkey.pub（不是 adbkey）的全部内容", ErrInvalid)
	}
	return key, nil
}

// AuthorizeADBKey 把电脑的 ADB 公钥加进手机（已经有了就不重复加）。手机要开着。
func (s *Service) AuthorizeADBKey(ctx context.Context, id, key string) error {
	key, err := checkADBKey(key)
	if err != nil {
		return err
	}
	if err := s.requireRunning(ctx, id, false); err != nil {
		return err
	}
	// 公钥作为位置参数传进去（$1），不拼进命令字符串，格式也已校验过。
	script := `f=` + adbKeysPath + `; mkdir -p /data/misc/adb && touch $f && (grep -qxF "$1" $f || echo "$1" >> $f) && chown system:shell $f && chmod 640 $f`
	if out, err := s.run.Run(ctx, "docker", "exec", containerName(id), "sh", "-c", script, "adbkey", key); err != nil {
		return fmt.Errorf("写入授权失败：%v %s", err, out)
	}
	return nil
}
