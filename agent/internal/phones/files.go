package phones

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// 从管理页往手机里放东西：安装 APK、上传文件到相册/下载目录。
// 文件先存到宿主机的临时目录，再 docker cp 进容器，用完就删。

// MaxUpload 单个文件最大 1 GB。
const MaxUpload = 1 << 30

// UploadDirs 允许上传到的手机目录（管理页的选项）。
var UploadDirs = map[string]string{
	"Download": "/sdcard/Download",
	"DCIM":     "/sdcard/DCIM/Camera",
	"Pictures": "/sdcard/Pictures",
	"Movies":   "/sdcard/Movies",
}

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N}._ -]+`)

// safeFileName 只留文字、数字、点、下划线、空格、横线；不允许路径。
func safeFileName(name string) (string, error) {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.TrimSpace(unsafeName.ReplaceAllString(name, "_"))
	name = strings.TrimLeft(name, ".")
	if name == "" || name == "_" {
		return "", fmt.Errorf("%w：文件名不对", ErrInvalid)
	}
	if len(name) > 120 {
		ext := path.Ext(name)
		name = name[:120-len(ext)] + ext
	}
	return name, nil
}

// stage 把上传的内容存到宿主机临时文件。
func (s *Service) stage(r io.Reader, name string) (string, func(), error) {
	dir := filepath.Join(s.cfg.DataDir, "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp(dir, "upload-*-"+name)
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	n, err := io.Copy(f, io.LimitReader(r, MaxUpload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if n > MaxUpload {
		cleanup()
		return "", nil, fmt.Errorf("%w：文件超过 1 GB", ErrInvalid)
	}
	if n == 0 {
		cleanup()
		return "", nil, fmt.Errorf("%w：文件是空的", ErrInvalid)
	}
	return f.Name(), cleanup, nil
}

// InstallAPK 安装 APK（已安装的会覆盖升级）。
func (s *Service) InstallAPK(ctx context.Context, id, fileName string, r io.Reader) (string, error) {
	if !strings.EqualFold(path.Ext(fileName), ".apk") {
		return "", fmt.Errorf("%w：只能安装 .apk 文件", ErrInvalid)
	}
	if err := s.requireRunning(ctx, id, true); err != nil {
		return "", err
	}
	local, cleanup, err := s.stage(r, "app.apk")
	if err != nil {
		return "", err
	}
	defer cleanup()
	return s.installStaged(ctx, id, local)
}

// installStaged 把已经存在宿主机上的 APK 装进一台手机（单台、批量都用它）。
func (s *Service) installStaged(ctx context.Context, id, local string) (string, error) {
	if err := s.requireRunning(ctx, id, true); err != nil {
		return "", err
	}
	name := containerName(id)
	remote := "/data/local/tmp/pp-install.apk"
	if out, err := s.run.Run(ctx, "docker", "cp", local, name+":"+remote); err != nil {
		return "", fmt.Errorf("传进手机失败：%v %s", err, out)
	}
	out, err := s.run.Run(ctx, "docker", "exec", name, "pm", "install", "-r", remote)
	_, _ = s.run.Run(ctx, "docker", "exec", name, "rm", "-f", remote)
	if err != nil || !strings.Contains(out, "Success") {
		return "", fmt.Errorf("安装失败：%s", strings.TrimSpace(out+" "+errText(err)))
	}
	return out, nil
}

// UploadFile 上传文件到手机目录，并通知相册刷新。返回手机里的路径。
func (s *Service) UploadFile(ctx context.Context, id, dirKey, fileName string, r io.Reader) (string, error) {
	dir, ok := UploadDirs[dirKey]
	if !ok {
		return "", fmt.Errorf("%w：不支持的目录 %q", ErrInvalid, dirKey)
	}
	safe, err := safeFileName(fileName)
	if err != nil {
		return "", err
	}
	if err := s.requireRunning(ctx, id, true); err != nil {
		return "", err
	}
	local, cleanup, err := s.stage(r, "file")
	if err != nil {
		return "", err
	}
	defer cleanup()
	return s.uploadStaged(ctx, id, dir, safe, local)
}

// uploadStaged 把已经存在宿主机上的文件放进一台手机的目录（单台、批量都用它）。
func (s *Service) uploadStaged(ctx context.Context, id, dir, safe, local string) (string, error) {
	if err := s.requireRunning(ctx, id, true); err != nil {
		return "", err
	}
	name := containerName(id)
	remote := dir + "/" + safe
	// 实测（踩坑 #16）：/sdcard 是安卓自己挂的虚拟目录，docker cp 看不到；真正的文件在 /data/media/0 下。
	backing := "/data/media/0" + strings.TrimPrefix(remote, "/sdcard")
	if out, err := s.run.Run(ctx, "docker", "exec", name, "mkdir", "-p", dir); err != nil {
		return "", fmt.Errorf("建目录失败：%v %s", err, out)
	}
	if out, err := s.run.Run(ctx, "docker", "cp", local, name+":"+backing); err != nil {
		return "", fmt.Errorf("传进手机失败：%v %s", err, out)
	}
	// docker cp 进去的文件属于 root；交给媒体存储用户，再让媒体库扫描一下（相册里才看得到）。
	_, _ = s.run.Run(ctx, "docker", "exec", name, "chown", "media_rw:media_rw", backing)
	_, _ = s.run.Run(ctx, "docker", "exec", name, "chmod", "664", backing)
	_, _ = s.run.Run(ctx, "docker", "exec", name, "am", "broadcast", "-a", "android.intent.action.MEDIA_SCANNER_SCAN_FILE", "-d", "file://"+remote)
	return remote, nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
