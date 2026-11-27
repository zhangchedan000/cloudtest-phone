package phones

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 虚拟定位（2026-11-27 用户要求，对齐魔云腾）：给手机设一个假的 GPS 位置，App 读定位就是这个点。
//
// 做法：用安卓自带的「测试定位 provider」（`cmd location providers`，不装 App）。给 shell 加一次
// mock_location 权限，就能往 gps / network / fused 三个 provider 塞位置——fused 是谷歌服务和多数 App 用的。
// 位置会定时重推（RunLocationRefresh），保持「新鲜」，不然有的 App 嫌定位太旧不认。
// 开了真 SELinux 的手机，这些命令走 docker exec 会自动转到命令小助手（和别的命令一样）。

// mockProviders 要设的三个定位源。
var mockProviders = []string{"gps", "network", "fused"}

// locationScript 生成设测试定位的脚本。first=true 连 provider 创建/开关/权限一起做；false 只刷新位置。
func locationScript(lat, lng float64, first bool) string {
	loc := strconv.FormatFloat(lat, 'f', 6, 64) + "," + strconv.FormatFloat(lng, 'f', 6, 64)
	var b strings.Builder
	if first {
		b.WriteString("cmd location set-location-enabled true\n")
		b.WriteString("appops set com.android.shell android:mock_location allow\n")
		for _, p := range mockProviders {
			fmt.Fprintf(&b, "cmd location providers add-test-provider %s\n", p)
			fmt.Fprintf(&b, "cmd location providers set-test-provider-enabled %s true\n", p)
		}
	}
	for _, p := range mockProviders {
		fmt.Fprintf(&b, "cmd location providers set-test-provider-location %s --location %s --accuracy 12\n", p, loc)
	}
	return b.String()
}

// pushLocation 把位置塞进手机（在跑才有意义）。
func (s *Service) pushLocation(ctx context.Context, id string, lat, lng float64, first bool) error {
	out, err := s.run.Run(ctx, "docker", "exec", containerName(id), "sh", "-c", locationScript(lat, lng, first))
	if err != nil {
		return fmt.Errorf("设虚拟定位失败：%v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// removeMock 撤掉测试定位（关定位时用）。
func (s *Service) removeMock(ctx context.Context, id string) {
	var b strings.Builder
	for _, p := range mockProviders {
		fmt.Fprintf(&b, "cmd location providers remove-test-provider %s\n", p)
	}
	_, _ = s.run.Run(ctx, "docker", "exec", containerName(id), "sh", "-c", b.String())
}

// applyLocation 开机后重设这台手机的虚拟定位（settings.go 的开机后流程调它）。没设位置就不管。
func (s *Service) applyLocation(ctx context.Context, id string) {
	st := s.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil || p.Location == nil || !s.isRunning(ctx, id) {
		return
	}
	_ = s.pushLocation(ctx, id, p.Location.Lat, p.Location.Lng, true)
}

// GetLocation 这台手机当前设的虚拟定位；没设返回 nil。
func (s *Service) GetLocation(id string) (*store.LatLng, error) {
	st := s.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil {
		return nil, err
	}
	return p.Location, nil
}

// SetLocation 设虚拟定位（经纬度），存下来并马上生效（在跑的话）。
func (s *Service) SetLocation(ctx context.Context, id string, lat, lng float64) error {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return fmt.Errorf("%w：经纬度超出范围", ErrInvalid)
	}
	if err := s.store.Update(func(st *store.State) error {
		p, err := st.Phone(id)
		if err != nil {
			return err
		}
		p.Location = &store.LatLng{Lat: lat, Lng: lng}
		return nil
	}); err != nil {
		return err
	}
	if s.isRunning(ctx, id) {
		return s.pushLocation(ctx, id, lat, lng, true)
	}
	return nil
}

// ClearLocation 关掉虚拟定位。
func (s *Service) ClearLocation(ctx context.Context, id string) error {
	if err := s.store.Update(func(st *store.State) error {
		p, err := st.Phone(id)
		if err != nil {
			return err
		}
		p.Location = nil
		return nil
	}); err != nil {
		return err
	}
	if s.isRunning(ctx, id) {
		s.removeMock(ctx, id)
	}
	return nil
}

// RunLocationRefresh 定时给所有「设了虚拟定位且在跑」的手机重推位置，保持定位新鲜（main.go 起）。
func (s *Service) RunLocationRefresh(ctx context.Context, every time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
			for _, p := range s.store.Snapshot().Phones {
				if p.Location == nil {
					continue
				}
				c, cancel := context.WithTimeout(ctx, 10*time.Second)
				if s.isRunning(c, p.ID) {
					_ = s.pushLocation(c, p.ID, p.Location.Lat, p.Location.Lng, false)
				}
				cancel()
			}
		}
	}
}
