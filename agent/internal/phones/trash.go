package phones

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"
)

// 回收站：删除和重置时，手机的旧数据挪到 DataDir/trash/<设备ID>[-reset]-<时间>，误操作还能找回。
// 这里负责列出、彻底删除、自动清理太旧的。

// TrashItem 回收站里的一份旧数据。
type TrashItem struct {
	Name    string    `json:"name"`
	PhoneID string    `json:"phoneId"`
	Kind    string    `json:"kind"` // delete（删除设备）/ reset（重置或刷机）
	At      time.Time `json:"at"`
	SizeMB  float64   `json:"sizeMB"`
}

var trashName = regexp.MustCompile(`^([0-9a-f]{6,32})(-reset)?-(\d{8}-\d{6})$`)

func (s *Service) trashDir() string { return filepath.Join(s.cfg.DataDir, "trash") }

// ListTrash 回收站里的东西，新的在前。
func (s *Service) ListTrash() ([]TrashItem, error) {
	entries, err := os.ReadDir(s.trashDir())
	if os.IsNotExist(err) {
		return []TrashItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []TrashItem{}
	for _, e := range entries {
		m := trashName.FindStringSubmatch(e.Name())
		if m == nil || !e.IsDir() {
			continue
		}
		at, _ := time.ParseInLocation("20060102-150405", m[3], time.Local)
		kind := "delete"
		if m[2] != "" {
			kind = "reset"
		}
		out = append(out, TrashItem{Name: e.Name(), PhoneID: m[1], Kind: kind, At: at, SizeMB: dirSizeMB(filepath.Join(s.trashDir(), e.Name()))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

func dirSizeMB(dir string) float64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				// 按实际占的磁盘块算：手机存储镜像是稀疏文件，表面大小 256GB，实际可能只有几百 MB
				if st, ok := info.Sys().(*syscall.Stat_t); ok {
					total += st.Blocks * 512
				} else {
					total += info.Size()
				}
			}
		}
		return nil
	})
	return float64(total) / (1 << 20)
}

// DeleteTrash 彻底删除回收站里的一份（名字必须是回收站的格式，不能带路径）。
func (s *Service) DeleteTrash(name string) error {
	if !trashName.MatchString(name) {
		return fmt.Errorf("%w：回收站里没有 %q", ErrInvalid, name)
	}
	dir := filepath.Join(s.trashDir(), name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("%w：回收站里没有 %q", ErrInvalid, name)
	}
	return os.RemoveAll(dir)
}

// PurgeTrash 彻底删除放进回收站超过 keep 的，返回删了几份。
func (s *Service) PurgeTrash(keep time.Duration) (int, error) {
	items, err := s.ListTrash()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if !it.At.IsZero() && s.now().Sub(it.At) > keep {
			if err := s.DeleteTrash(it.Name); err == nil {
				n++
			}
		}
	}
	return n, nil
}
