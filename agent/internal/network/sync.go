package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// Syncer 把最新配置写成文件，并让正在运行的 mihomo 重新加载。配置没变就什么都不做。
type Syncer struct {
	Path   string       // 配置文件位置，例如 /opt/prism-phone/data/mihomo/config.yaml
	Opt    Options      // 生成配置的参数
	Client *http.Client // 测试时换掉

	mu      sync.Mutex
	last    string
	blocked map[string]string
	err     error
}

// Blocked 上次同步时没分到出口的设备（设备 ID → 原因）。
func (s *Syncer) Blocked() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.blocked))
	for k, v := range s.blocked {
		out[k] = v
	}
	return out
}

// LastError 上次同步的错误（mihomo 没在跑、配置被拒绝……），nil 表示正常。
func (s *Syncer) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Sync 生成配置；有变化就写文件并通知 mihomo 重新加载。
func (s *Syncer) Sync(ctx context.Context, st store.State) error {
	res := Build(st, s.Opt)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocked = res.Blocked
	if res.YAML == s.last && s.err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		s.err = err
		return err
	}
	// 配置里有代理密码：只有 root 能读。先写临时文件再改名，写一半断电也不会坏。
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(res.YAML), 0o600); err != nil {
		s.err = err
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		s.err = err
		return err
	}
	s.err = s.reload(ctx)
	if s.err == nil {
		s.last = res.YAML
	}
	return s.err
}

// reload 调 mihomo 的控制接口：PUT /configs?force=true {"path": …}。
func (s *Syncer) reload(ctx context.Context) error {
	if s.Opt.APIListen == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{"path": s.Path})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://"+s.Opt.APIListen+"/configs?force=true", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("网络模块（mihomo）没在运行：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&msg)
		return fmt.Errorf("网络模块拒绝了新配置（%d）：%s", resp.StatusCode, msg.Message)
	}
	return nil
}
