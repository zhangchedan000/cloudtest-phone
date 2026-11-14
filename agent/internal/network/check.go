package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/proxycheck"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// CanCheck 网络模块开着并且有检测入口时，管家程序能经 mihomo 检测任何协议的节点。
func (s *Syncer) CanCheck() bool { return s.Opt.APIListen != "" && s.Opt.CheckPort > 0 }

// Check 经 mihomo 检测一个节点：同步配置 → 把 pp-check 分组切到这个节点 → 经本地检测入口查出口 IP → 切回 REJECT。
// 同一时间只测一个（共用一个检测入口）。
func (s *Syncer) Check(ctx context.Context, st store.State, px store.Proxy) store.ProxyCheck {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	res := store.ProxyCheck{CheckedAt: time.Now()}
	if _, err := outbound(px); err != nil {
		res.Error = err.Error()
		return res
	}
	if err := s.Sync(ctx, st); err != nil {
		res.Error = err.Error()
		return res
	}
	if err := s.selectCheck(ctx, "px-"+px.ID); err != nil {
		res.Error = err.Error()
		return res
	}
	defer func() { _ = s.selectCheck(context.Background(), "REJECT") }()
	return proxycheck.Check(ctx, store.Proxy{Protocol: "http", Address: "http://127.0.0.1:" + strconv.Itoa(s.Opt.CheckPort)})
}

func (s *Syncer) selectCheck(ctx context.Context, name string) error {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://"+s.Opt.APIListen+"/proxies/"+CheckGroup, bytes.NewReader(body))
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
		return fmt.Errorf("网络模块切换检测节点失败（%d）", resp.StatusCode)
	}
	return nil
}
