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

// storageBytes 手机 /data 的大小（字节）；没填容量返回 0。
// 真机 /data 比标称容量小一截（系统分区占了，256GB 的手机 /data 约 241GB；设置里照样显示 256GB，安卓按 2 的幂往上取整）。
// 容量就是机型的标称容量、机型包录了真机 /data 字节数（dataBytes）时照真机；否则按标称容量的 94%。
func storageBytes(byKey map[string]any, m *Model) int64 {
	gb, ok := toInt(byKey["storageTotal"])
	if !ok || gb <= 0 || gb > 2048 {
		return 0
	}
	if m != nil && m.DataBytes > 0 {
		if mg, ok := toInt(m.Values["storageTotal"]); ok && mg == gb && m.DataBytes <= int64(gb)*1_000_000_000 {
			return m.DataBytes
		}
	}
	return int64(gb) * 940_000_000
}

func isMounted(ctx context.Context, run Runner, dir string) bool {
	_, err := run.Run(ctx, "mountpoint", "-q", dir)
	return err == nil
}

// dataWritable 停用了的文件系统读目录还行（有缓存），写就 I/O 错误：用建一个空文件试
func dataWritable(dir string) bool {
	p := filepath.Join(dir, ".pp_alive")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		return false
	}
	_ = os.Remove(p)
	return true
}

func dirEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err != nil || len(entries) == 0
}

// prepareData 开机前准备 /data：有镜像就挂上；新手机（数据目录空的）按机型容量建镜像；
// 老手机（目录里已有数据、没有镜像）照旧用目录。返回要挂进容器的宿主机目录。
func (s *Service) prepareData(ctx context.Context, id string, byKey map[string]any, m *Model) (string, error) {
	dir, img := s.dataDir(id), s.dataImage(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if !s.cfg.DataImages {
		return dir, nil
	}
	if isMounted(ctx, s.run, dir) {
		if dataWritable(dir) {
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
		size := storageBytes(byKey, m)
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
