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
	checkMu sync.Mutex
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

// CloseConnections 断开某台手机（按来源 IP）现在开着的所有连接（mihomo 控制接口 GET / DELETE /connections）。
// 切换代理后用：mihomo 只把新连接按新规则分流，已经建好的长连接（谷歌登录页、推送等）还挂在老节点上，
// 同一次登录里出现两个出口 IP，网站会当成可疑（踩坑 #70）。断开后 App 会用新节点重连。返回断开了几条。
func (s *Syncer) CloseConnections(ctx context.Context, srcIP string) (int, error) {
	if s.Opt.APIListen == "" || srcIP == "" {
		return 0, nil
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+s.Opt.APIListen+"/connections", nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("网络模块（mihomo）没在运行：%w", err)
	}
	var list struct {
		Connections []struct {
			ID       string `json:"id"`
			Metadata struct {
				SourceIP string `json:"sourceIP"`
			} `json:"metadata"`
		} `json:"connections"`
	}
	err = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if err != nil {
		return 0, fmt.Errorf("读连接列表：%w", err)
	}
	n := 0
	for _, c := range list.Connections {
		if c.Metadata.SourceIP != srcIP || c.ID == "" {
			continue
		}
		del, err := http.NewRequestWithContext(ctx, http.MethodDelete, "http://"+s.Opt.APIListen+"/connections/"+c.ID, nil)
		if err != nil {
			continue
		}
		if r, err := client.Do(del); err == nil {
			r.Body.Close()
			if r.StatusCode < 300 {
				n++
			}
		}
	}
	return n, nil
}
