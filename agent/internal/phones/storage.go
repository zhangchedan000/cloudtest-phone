package phones

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// 存储容量（踩坑 #53）：以前每台手机的 /data 是宿主机上的一个普通目录，手机里看到的总容量是宿主机整块盘
// （开发机 183GB），真手机是 128 / 256 / 512GB。现在给新建（或重置后）的手机一个按机型容量大小的
// ext4 磁盘镜像，开机前挂到 /data。镜像是稀疏文件：只有真正写进去的数据才占宿主机空间（空手机几十 MB）。
// 已有的手机（数据目录已经有东西、没有镜像）保持原样，不迁移、不碰数据；重置之后自动换成镜像。
// 宿主机没有 mkfs.ext4 / mount，或机型没填存储容量，就照旧用目录。

func (s *Service) phoneDir(id string) string { return filepath.Join(s.cfg.DataDir, "phones", id) }
func (s *Service) dataDir(id string) string  { return filepath.Join(s.phoneDir(id), "data") }
func (s *Service) dataImage(id string) string {
	return filepath.Join(s.phoneDir(id), "data.img")
}

// DataImagesSupported 宿主机能做磁盘镜像吗（root + mkfs.ext4 + mount）。
func DataImagesSupported() bool {
	if os.Geteuid() != 0 {
		return false
	}
	for _, c := range []string{"mkfs.ext4", "mount", "umount", "mountpoint"} {
		if _, err := exec.LookPath(c); err != nil {
			return false
		}
	}
	return true
}

// storageBytes 机型的存储容量（GB，十进制，和手机厂商标的一样）换成字节；没填返回 0。
func storageBytes(byKey map[string]any) int64 {
	gb, ok := toInt(byKey["storageTotal"])
	if !ok || gb <= 0 || gb > 2048 {
		return 0
	}
	return int64(gb) * 1_000_000_000
}

func isMounted(ctx context.Context, run Runner, dir string) bool {
	_, err := run.Run(ctx, "mountpoint", "-q", dir)
	return err == nil
}

func dirEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err != nil || len(entries) == 0
}

// prepareData 开机前准备 /data：有镜像就挂上；新手机（数据目录空的）按机型容量建镜像；
// 老手机（目录里已有数据、没有镜像）照旧用目录。返回要挂进容器的宿主机目录。
func (s *Service) prepareData(ctx context.Context, id string, byKey map[string]any) (string, error) {
	dir, img := s.dataDir(id), s.dataImage(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if !s.cfg.DataImages {
		return dir, nil
	}
	if isMounted(ctx, s.run, dir) {
		if _, err := os.ReadDir(dir); err == nil {
			return dir, nil
		}
		// 安卓自己关机 / 重启时会对 /data 发 F2FS_IOC_SHUTDOWN（和 EXT4_IOC_SHUTDOWN 是同一个号）：先刷盘、再把文件系统
		// 停掉。这块盘是宿主机挂的，停掉以后一直 I/O 错误（踩坑 #135），重新挂一遍就好（数据在停之前已经刷进去了）
		log.Printf("手机 %s：存储读不了（安卓关机时停用了文件系统），重新挂载", id)
		if _, err := s.run.Run(ctx, "umount", dir); err != nil {
			if out, err := s.run.Run(ctx, "umount", "-l", dir); err != nil {
				return "", fmt.Errorf("卸载手机存储失败：%v %s", err, out)
			}
		}
	}
	if _, err := os.Stat(img); os.IsNotExist(err) {
		size := storageBytes(byKey)
		if size == 0 || !dirEmpty(dir) {
			return dir, nil // 没填容量，或老手机已有数据：不动
		}
		if err := createImage(ctx, s.run, img, size); err != nil {
			_ = os.Remove(img)
			return "", err
		}
	}
	if out, err := s.run.Run(ctx, "mount", "-o", "loop,noatime", img, dir); err != nil {
		return "", fmt.Errorf("挂载手机存储失败：%v %s", err, out)
	}
	return dir, nil
}

// createImage 建稀疏文件并格式化成 ext4（不预留 root 空间、inode 表延迟初始化：建得快、几乎不占空间）。
func createImage(ctx context.Context, run Runner, img string, size int64) error {
	f, err := os.OpenFile(img, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("建手机存储镜像：%w", err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return fmt.Errorf("建手机存储镜像：%w", err)
	}
	f.Close()
	if out, err := run.Run(ctx, "mkfs.ext4", "-q", "-F", "-m", "0", "-E", "lazy_itable_init=1,lazy_journal_init=1", img); err != nil {
		return fmt.Errorf("格式化手机存储：%v %s", err, out)
	}
	return nil
}

// releaseData 卸掉手机存储（删除 / 重置前，容器已经停了）。没挂就什么都不做。
func (s *Service) releaseData(ctx context.Context, id string) error {
	dir := s.dataDir(id)
	if !s.cfg.DataImages || !isMounted(ctx, s.run, dir) {
		return nil
	}
	if out, err := s.run.Run(ctx, "umount", dir); err != nil {
		// 可能还有进程占着（比如刚停的容器还没释放），退而求其次：懒卸载
		if out2, err2 := s.run.Run(ctx, "umount", "-l", dir); err2 != nil {
			return fmt.Errorf("卸载手机存储失败：%v %s %s", err, strings.TrimSpace(out), out2)
		}
	}
	return nil
}
