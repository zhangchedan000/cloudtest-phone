package phones

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 备份和恢复（2026-11-16 用户要求，参考魔云腾）：把一台手机的全部数据（装的 App、登录状态、相册……）存一份，
// 以后恢复到这台，或者复制到另一台同安卓版本的手机。
//
// 数据就是手机的 /data：新手机是一个 ext4 稀疏磁盘镜像（data.img），老手机是一个目录。
// 备份时手机必须是关着的（开着的话先体面关机，备份完再开），否则数据库写到一半，备份出来的数据可能是坏的。
// 备份放在 DataDir/backups/<编号>/：meta.json + data.img（稀疏复制，只占实际写过的空间）或 data.tar（老手机）。
// 恢复前，目标手机现在的数据挪进回收站（恢复错了还能找回）。

// DataJob 一台手机正在做（或最近做完）的备份 / 恢复。备份恢复要关机、拷几个 GB，几十秒到几分钟，
// 在后台做，管理页轮询手机列表看进度（HTTPS 反向代理一般 60 秒就断请求）。
type DataJob struct {
	Kind     string    `json:"kind"`  // backup / restore
	State    string    `json:"state"` // running / done / failed
	Error    string    `json:"error,omitempty"`
	BackupID string    `json:"backupId,omitempty"`
	At       time.Time `json:"at"`
}

// JobRunning 这台手机正在备份或恢复吗。
func (s *Service) JobRunning(id string) bool { return s.jobRunning(id) }

func (s *Service) jobRunning(id string) bool {
	j := s.dataJobs.get(id)
	return j != nil && j.State == "running"
}

// StartBackup 在后台备份（马上返回）。
func (s *Service) StartBackup(id, note string) error {
	st := s.store.Snapshot()
	if _, err := st.Phone(id); err != nil {
		return err
	}
	if s.jobRunning(id) {
		return fmt.Errorf("%w：这台正在备份或恢复，等它做完", ErrInvalid)
	}
	if _, err := os.Stat(s.dataImage(id)); err != nil && dirEmpty(s.dataDir(id)) {
		return fmt.Errorf("%w：这台手机还没有数据（没开过机），不用备份", ErrInvalid)
	}
	s.dataJobs.put(id, DataJob{Kind: "backup", State: "running", At: s.now().UTC()})
	go func() {
		b, err := s.CreateBackup(context.Background(), id, note)
		j := DataJob{Kind: "backup", State: "done", BackupID: b.ID, At: s.now().UTC()}
		if err != nil {
			j.State, j.Error = "failed", err.Error()
			log.Printf("手机 %s：备份失败：%v", id, err)
		}
		s.dataJobs.put(id, j)
	}()
	return nil
}

// StartRestore 在后台恢复（先检查能不能恢复，马上返回）。
func (s *Service) StartRestore(id, bid string) error {
	if err := s.checkRestore(id, bid); err != nil {
		return err
	}
	if s.jobRunning(id) {
		return fmt.Errorf("%w：这台正在备份或恢复，等它做完", ErrInvalid)
	}
	s.dataJobs.put(id, DataJob{Kind: "restore", State: "running", BackupID: bid, At: s.now().UTC()})
	go func() {
		err := s.RestoreBackup(context.Background(), id, bid)
		j := DataJob{Kind: "restore", State: "done", BackupID: bid, At: s.now().UTC()}
		if err != nil {
			j.State, j.Error = "failed", err.Error()
			log.Printf("手机 %s：恢复备份失败：%v", id, err)
		}
		s.dataJobs.put(id, j)
	}()
	return nil
}

// Backup 一份备份。
type Backup struct {
	ID             string    `json:"id"`
	PhoneID        string    `json:"phoneId"`
	PhoneName      string    `json:"phoneName"`
	ModelID        string    `json:"modelId"`
	AndroidVersion int       `json:"androidVersion"`
	Gapps          bool      `json:"gapps"`
	Note           string    `json:"note,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	Kind           string    `json:"kind"` // image / dir
	SizeMB         float64   `json:"sizeMB"`
}

func (s *Service) backupsDir() string { return filepath.Join(s.cfg.DataDir, "backups") }

// usedBytes 文件实际占的磁盘空间（稀疏文件按块算）。
func usedBytes(path string) int64 {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0
	}
	return st.Blocks * 512
}

func freeBytes(dir string) int64 {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dir, &fs); err != nil {
		return -1
	}
	return int64(fs.Bavail) * int64(fs.Bsize)
}

// checkSpace 要拷 need 字节，盘上至少还得剩 2GB。
func checkSpace(dir string, need int64) error {
	if free := freeBytes(dir); free >= 0 && free < need+2<<30 {
		return fmt.Errorf("%w：硬盘空间不够（要 %.1f GB，只剩 %.1f GB）", ErrInvalid, float64(need)/(1<<30), float64(free)/(1<<30))
	}
	return nil
}

// stopForData 手机开着就先体面关机并卸掉存储。返回原来是不是开着的。
func (s *Service) stopForData(ctx context.Context, id string) (bool, error) {
	was := s.isRunning(ctx, id)
	if was {
		if err := s.Stop(ctx, id); err != nil {
			return false, fmt.Errorf("关机失败：%w", err)
		}
	}
	if err := s.docker.RemoveContainer(ctx, containerName(id)); err != nil {
		return was, fmt.Errorf("停掉旧容器：%w", err)
	}
	return was, s.releaseData(ctx, id)
}

// CreateBackup 备份一台手机（开着就先关机，备份完再开回来）。
func (s *Service) CreateBackup(ctx context.Context, id, note string) (Backup, error) {
	st := s.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil {
		return Backup{}, err
	}
	b := Backup{PhoneID: p.ID, PhoneName: p.Name, ModelID: p.ModelID, AndroidVersion: p.AndroidVersion, Gapps: p.Gapps,
		Note: strings.TrimSpace(note), CreatedAt: s.now().UTC()}
	var rnd [3]byte
	_, _ = rand.Read(rnd[:])
	b.ID = s.now().Format("20060102-150405") + "-" + hex.EncodeToString(rnd[:])
	img, dir := s.dataImage(id), s.dataDir(id)
	_, imgErr := os.Stat(img)
	if imgErr != nil && dirEmpty(dir) {
		return Backup{}, fmt.Errorf("%w：这台手机还没有数据（没开过机），不用备份", ErrInvalid)
	}
	if err := os.MkdirAll(s.backupsDir(), 0o700); err != nil {
		return Backup{}, err
	}
	wasRunning, err := s.stopForData(ctx, id)
	restart := func() {
		if wasRunning {
			if err := s.Start(context.WithoutCancel(ctx), id); err != nil {
				log.Printf("手机 %s：备份后开机失败：%v", id, err)
			}
		}
	}
	if err != nil {
		restart()
		return Backup{}, err
	}
	dest := filepath.Join(s.backupsDir(), b.ID)
	if err := os.MkdirAll(dest, 0o700); err != nil {
		restart()
		return Backup{}, err
	}
	if imgErr == nil {
		b.Kind = "image"
		if err = checkSpace(s.backupsDir(), usedBytes(img)); err == nil {
			_, err = s.runOut(ctx, "cp", "--sparse=always", img, filepath.Join(dest, "data.img"))
		}
	} else {
		b.Kind = "dir"
		if err = checkSpace(s.backupsDir(), int64(dirSizeMB(dir)*(1<<20))); err == nil {
			_, err = s.runOut(ctx, "tar", "--numeric-owner", "--xattrs", "--sparse", "-cpf", filepath.Join(dest, "data.tar"), "-C", dir, ".")
		}
	}
	restart()
	if err != nil {
		_ = os.RemoveAll(dest)
		return Backup{}, fmt.Errorf("备份失败：%w", err)
	}
	b.SizeMB = backupSizeMB(dest)
	meta, _ := json.MarshalIndent(b, "", "  ")
	if err := os.WriteFile(filepath.Join(dest, "meta.json"), meta, 0o600); err != nil {
		_ = os.RemoveAll(dest)
		return Backup{}, err
	}
	return b, nil
}

func backupSizeMB(dir string) float64 {
	var total int64
	for _, f := range []string{"data.img", "data.tar"} {
		total += usedBytes(filepath.Join(dir, f))
	}
	return float64(total) / (1 << 20)
}

// ListBackups 全部备份，新的在前。
func (s *Service) ListBackups() ([]Backup, error) {
	ents, err := os.ReadDir(s.backupsDir())
	if errors.Is(err, os.ErrNotExist) {
		return []Backup{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Backup{}
	for _, e := range ents {
		raw, err := os.ReadFile(filepath.Join(s.backupsDir(), e.Name(), "meta.json"))
		if err != nil {
			continue // 正在备份的（还没写 meta）或坏的，不列
		}
		var b Backup
		if json.Unmarshal(raw, &b) == nil && b.ID == e.Name() {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) getBackup(bid string) (Backup, error) {
	if bid == "" || strings.ContainsAny(bid, "/\\") || bid == "." || bid == ".." {
		return Backup{}, fmt.Errorf("%w：备份编号不对", ErrInvalid)
	}
	raw, err := os.ReadFile(filepath.Join(s.backupsDir(), bid, "meta.json"))
	if err != nil {
		return Backup{}, fmt.Errorf("%w：没有这个备份", store.ErrNotFound)
	}
	var b Backup
	if err := json.Unmarshal(raw, &b); err != nil {
		return Backup{}, err
	}
	return b, nil
}

// DeleteBackup 删掉一份备份。
func (s *Service) DeleteBackup(bid string) error {
	if _, err := s.getBackup(bid); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.backupsDir(), bid))
}

// RestoreBackup 把备份恢复到一台手机（可以是原来那台，也可以是别的同安卓版本、同谷歌服务设置的手机）。
// 目标手机现在的数据先挪进回收站；开着的先关机，恢复完再开。
func (s *Service) RestoreBackup(ctx context.Context, id, bid string) error {
	if err := s.checkRestore(id, bid); err != nil {
		return err
	}
	b, _ := s.getBackup(bid)
	src := filepath.Join(s.backupsDir(), bid)
	if err := checkSpace(s.cfg.DataDir, usedBytes(filepath.Join(src, "data.img"))+usedBytes(filepath.Join(src, "data.tar"))); err != nil {
		return err
	}
	wasRunning, err := s.stopForData(ctx, id)
	if err != nil {
		return err
	}
	if err := s.trashData(id, "restore"); err != nil {
		return err
	}
	if err := os.MkdirAll(s.phoneDir(id), 0o755); err != nil {
		return err
	}
	switch b.Kind {
	case "image":
		_, err = s.runOut(ctx, "cp", "--sparse=always", filepath.Join(src, "data.img"), s.dataImage(id))
	default:
		if err = os.MkdirAll(s.dataDir(id), 0o771); err == nil {
			_, err = s.runOut(ctx, "tar", "--numeric-owner", "--xattrs", "-xpf", filepath.Join(src, "data.tar"), "-C", s.dataDir(id))
		}
	}
	if err != nil {
		return fmt.Errorf("恢复失败（原来的数据在回收站里）：%w", err)
	}
	if wasRunning {
		return s.Start(context.WithoutCancel(ctx), id)
	}
	return nil
}

// checkRestore 这份备份能不能恢复到这台手机。
func (s *Service) checkRestore(id, bid string) error {
	b, err := s.getBackup(bid)
	if err != nil {
		return err
	}
	st := s.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil {
		return err
	}
	if p.AndroidVersion != b.AndroidVersion {
		return fmt.Errorf("%w：备份是 Android %d，这台是 Android %d，不能恢复（系统版本不同，App 数据不通用）", ErrInvalid, b.AndroidVersion, p.AndroidVersion)
	}
	if p.Gapps != b.Gapps {
		return fmt.Errorf("%w：备份%s谷歌服务，这台%s，不能恢复（先把这台的谷歌服务开关调成一样）", ErrInvalid, map[bool]string{true: "带", false: "不带"}[b.Gapps], map[bool]string{true: "带", false: "不带"}[p.Gapps])
	}
	return nil
}

// runOut 跑命令，出错时带上输出。
func (s *Service) runOut(ctx context.Context, name string, args ...string) (string, error) {
	out, err := s.run.Run(ctx, name, args...)
	if err != nil {
		return out, fmt.Errorf("%v %s", err, strings.TrimSpace(out))
	}
	return out, nil
}
