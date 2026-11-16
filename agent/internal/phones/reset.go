package phones

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// ResetRequest 一键重置 / 一键刷机。
//
//   - 一键重置：清空这台安卓的全部数据（App、账号、相册），恢复成刚出厂的样子。
//   - Renew=true：同时重新随机「随机」字段（相当于换一台新手机，机型不变）。
//   - AndroidVersion 不为 0：同时换安卓版本（一键刷机）。
//
// 旧数据不直接删，挪到回收站（DataDir/trash），误操作还能找回。
type ResetRequest struct {
	Renew          bool `json:"renew"`
	AndroidVersion int  `json:"androidVersion"`
}

func (s *Service) Reset(ctx context.Context, id string, req ResetRequest) error {
	st := s.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil {
		return err
	}
	if req.AndroidVersion != 0 {
		if _, ok := s.cfg.Images[req.AndroidVersion]; !ok {
			return fmt.Errorf("%w：这台机器没有 Android %d 的镜像", ErrInvalid, req.AndroidVersion)
		}
	}
	wasRunning := s.isRunning(ctx, id)
	if err := s.docker.RemoveContainer(ctx, containerName(id)); err != nil {
		return fmt.Errorf("停掉旧容器：%w", err)
	}
	s.teardownWifi(ctx, p.ID)
	if err := s.releaseData(ctx, p.ID); err != nil {
		return err
	}
	if err := s.trashData(p.ID, "reset"); err != nil {
		return err
	}
	if err := s.store.Update(func(st *store.State) error {
		ph, err := st.Phone(id)
		if err != nil {
			return err
		}
		if req.AndroidVersion != 0 {
			ph.AndroidVersion = req.AndroidVersion
		}
		if req.Renew {
			m := s.model(ph.ModelID)
			for key, fs := range ph.Fields {
				if fs.Mode == "random" && !fs.Locked {
					fs.Value = RandomValue(key, m)
					ph.Fields[key] = fs
				}
			}
		}
		ph.LastActiveAt = nil
		return nil
	}); err != nil {
		return err
	}
	if wasRunning {
		return s.Start(ctx, id)
	}
	return nil
}

// trashData 把一台手机现在的数据（磁盘镜像或目录）整个挪进回收站（容器已停、存储已卸载）。没有数据就什么都不做。
func (s *Service) trashData(id, tag string) error {
	dataDir := s.dataDir(id)
	dest := filepath.Join(s.cfg.DataDir, "trash", id+"-"+tag+"-"+s.now().Format("20060102-150405"))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(s.dataImage(id)); err == nil {
		// 用磁盘镜像的手机：镜像整个挪进回收站，空的挂载点删掉
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		if err := os.Rename(s.dataImage(id), filepath.Join(dest, "data.img")); err != nil {
			return fmt.Errorf("旧数据挪到回收站：%w", err)
		}
		_ = os.Remove(dataDir)
	} else if _, err := os.Stat(dataDir); err == nil {
		if err := os.Rename(dataDir, dest); err != nil {
			return fmt.Errorf("旧数据挪到回收站：%w", err)
		}
	}
	return nil
}
