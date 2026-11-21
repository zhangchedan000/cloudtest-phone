package phones

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// IMEI 补丁（踩坑 #115）：框架 getImei() 走 Cuttlefish RIL（/vendor/lib64/libril-modem-lib.so）的
// RefRadioModem::getImei，它返回写死的 IMEI，不查管家的 AT+CGSN。管家本就按台生成唯一 IMEI（simSpec），
// 只是被这层盖掉没用上。做法：建机时把该台唯一 IMEI 打进一份 libril-modem-lib.so 副本（替换那 15 字节），
// 开机前只读 bind-mount 到 /vendor/lib64/libril-modem-lib.so（和 build.prop 改写一个机制，buildprop.go）。
//
// 安卓 14：IMEI 是 .rodata 里一个孤立的 15 位数字串（null 包住），直接改字符串。
// 安卓 15/16：IMEI 是代码里的立即数（movz/movk 拼栈），这里的字符串定位找不到唯一 15 位串 → off<0 → 跳过，
//            不打补丁、不报错（照镜像原样开机，留到后续立即数补丁）。
// 任何一步失败都只记日志、照镜像原样开机，不挡开机、不影响老手机。

const rilLibPath = "/vendor/lib64/libril-modem-lib.so"

// imei15 匹配 .rodata 里被 \x00 包住的 15 位纯数字串（getImei 的默认 IMEI）。
var imei15 = regexp.MustCompile(`\x00([0-9]{15})\x00`)

// imageRilLib 取镜像里的 libril-modem-lib.so、定位默认 IMEI 字符串偏移，缓存在 DataDir/cache/rillib/<镜像ID>/。
// 返回 base.so 路径和 IMEI 偏移（镜像没这个库、或定位不到唯一的 15 位串 → off=-1）。
func (s *Service) imageRilLib(ctx context.Context, image string) (base string, off int64, err error) {
	id, err := s.run.Run(ctx, "docker", "image", "inspect", "-f", "{{.Id}}", image)
	if err != nil || strings.TrimSpace(id) == "" {
		return "", -1, fmt.Errorf("读镜像 ID：%v %s", err, id)
	}
	short := strings.TrimPrefix(strings.TrimSpace(id), "sha256:")
	dir := filepath.Join(s.cfg.DataDir, "cache", "rillib", short)
	base = filepath.Join(dir, "base.so")
	meta := filepath.Join(dir, "off")
	if b, err := os.ReadFile(meta); err == nil {
		var o int64
		fmt.Sscan(strings.TrimSpace(string(b)), &o)
		return base, o, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", -1, err
	}
	tmp := "pp-rillib-" + short[:12]
	_, _ = s.run.Run(ctx, "docker", "rm", "-f", tmp)
	if out, err := s.run.Run(ctx, "docker", "create", "--name", tmp, image); err != nil {
		return "", -1, fmt.Errorf("建临时容器：%v %s", err, out)
	}
	defer s.run.Run(context.Background(), "docker", "rm", "-f", tmp)
	if _, err := s.run.Run(ctx, "docker", "cp", tmp+":"+rilLibPath, base); err != nil {
		// 镜像没这个库（没加电话层的旧镜像）：记成 off=-1，以后不再重试
		_ = os.WriteFile(meta, []byte("-1"), 0o644)
		return base, -1, nil
	}
	data, err := os.ReadFile(base)
	if err != nil {
		return "", -1, err
	}
	off = -1
	if ms := imei15.FindAllSubmatchIndex(data, -1); len(ms) == 1 {
		off = int64(ms[0][2]) // 捕获组起始 = 15 位数字第一位的偏移
	}
	// 唯一命中才改（0 个=安卓15/16立即数或没库；多个=不确定，都不动，安全）
	_ = os.WriteFile(meta, []byte(fmt.Sprint(off)), 0o644)
	return base, off, nil
}

// prepareImei 生成这台手机打了 IMEI 补丁的 libril-modem-lib.so，返回要加的挂载（宿主机文件:容器路径:ro）。
// imei 必须是 15 位数字；镜像定位不到字符串型 IMEI（off<0，含没库/15/16）时返回空串（不打补丁、不报错）。
func (s *Service) prepareImei(ctx context.Context, id, image, imei string) (string, error) {
	if len(imei) != 15 || strings.TrimLeft(imei, "0123456789") != "" {
		return "", fmt.Errorf("IMEI 不是 15 位数字：%q", imei)
	}
	base, off, err := s.imageRilLib(ctx, image)
	if err != nil {
		return "", err
	}
	if off < 0 {
		return "", nil // 这个镜像的 IMEI 不是字符串型（安卓15/16 立即数）或没这个库：先不处理
	}
	data, err := os.ReadFile(base)
	if err != nil {
		return "", err
	}
	if off+15 > int64(len(data)) {
		return "", fmt.Errorf("IMEI 偏移越界 off=%d len=%d", off, len(data))
	}
	copy(data[off:off+15], []byte(imei))
	dir := filepath.Join(s.phoneDir(id), "ril")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "libril-modem-lib.so")
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return "", err
	}
	return out + ":" + rilLibPath + ":ro", nil
}
