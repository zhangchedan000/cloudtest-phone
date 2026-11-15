package phones

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// 开机后设置：时区、语言。
// 实测（踩坑 #6）：persist.* 属性用 Redroid 启动参数设不进去，只能等安卓开机完成后在容器里 setprop。
// 语言要重启安卓框架（zygote）才生效，所以只在和当前值不同时设置，避免每次开机都多重启一次。

// SettingsStatus 一台设备开机后设置的结果（给管理页看）。
type SettingsStatus struct {
	State   string    `json:"state"` // pending / applied / failed
	Applied []string  `json:"applied,omitempty"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
}

// liveProps 开机后才能设置的字段 → 属性名。值必须匹配对应格式（也防止把奇怪的字符传进容器）。
var liveProps = map[string]struct {
	prop    string
	pattern *regexp.Regexp
	restart bool
}{
	"timezone": {"persist.sys.timezone", regexp.MustCompile(`^[A-Za-z]+(?:/[A-Za-z0-9_+-]+){0,2}$`), false},
	"locale":   {"persist.sys.locale", regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Za-z0-9]{2,8}){0,2}$`), true},
}

func isLiveProp(key string) bool {
	_, ok := liveProps[key]
	return ok
}

// safeSettingValue 允许写进 settings 的值：可见 ASCII + 常见符号，不含控制字符，长度有限。
// settings put 的参数不过 shell（docker exec 直接带参），这里主要挡控制字符和超长值。
func safeSettingValue(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// waitBooted 等安卓开机完成；超时或设备停了返回 false。
func (s *Service) waitBooted(ctx context.Context, id string) bool {
	deadline := time.Now().Add(s.bootTimeout())
	for {
		switch s.androidState(ctx, id) {
		case "running":
			return true
		case "stopped":
			return false
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(s.pollEvery())
	}
}

// applySettings 开机完成后把时区、语言设进去。在后台跑，结果记在 settings 里。
func (s *Service) applySettings(ctx context.Context, id string) {
	s.settings.set(id, SettingsStatus{State: "pending"})
	if !s.waitBooted(ctx, id) {
		s.settings.set(id, SettingsStatus{State: "failed", Error: "等开机超时或设备已关机"})
		return
	}
	st := s.store.Snapshot()
	p, err := st.Phone(id)
	if err != nil {
		s.settings.set(id, SettingsStatus{State: "failed", Error: err.Error()})
		return
	}
	name := containerName(id)
	var applied, problems []string
	restart := false
	for _, r := range s.resolve(*p, st) {
		def, ok := liveProps[r.Key]
		if !ok || r.Value == nil {
			continue
		}
		want := fmt.Sprint(r.Value)
		if !def.pattern.MatchString(want) {
			problems = append(problems, fmt.Sprintf("%s 的值 %q 格式不对，没设置", r.Key, want))
			continue
		}
		current, _ := s.run.Run(ctx, "docker", "exec", name, "getprop", def.prop)
		if strings.TrimSpace(current) == want {
			applied = append(applied, r.Key)
			continue
		}
		if out, err := s.run.Run(ctx, "docker", "exec", name, "setprop", def.prop, want); err != nil {
			problems = append(problems, fmt.Sprintf("%s 设置失败：%v %s", r.Key, err, out))
			continue
		}
		if r.Key == "timezone" {
			// 让正在运行的系统马上换时区（不支持这个命令的版本忽略，属性已经设好，重启后也会生效）。
			_, _ = s.run.Run(ctx, "docker", "exec", name, "cmd", "time_zone_detector", "suggest_manual_time_zone", "--zone_id", want)
		}
		applied = append(applied, r.Key)
		restart = restart || def.restart
	}
	// setting 类字段：开机后用安卓 settings put 写（android_id、设备名、蓝牙 MAC 等）。
	for _, r := range s.resolve(*p, st) {
		f, ok := s.cat.Field(r.Key)
		if !ok || f.Sink.Kind != "setting" || r.Value == nil {
			continue
		}
		val := fmt.Sprint(r.Value)
		if !safeSettingValue(val) {
			problems = append(problems, fmt.Sprintf("%s 的值 %q 含非法字符，没设置", r.Key, val))
			continue
		}
		wrote := false
		for _, sink := range f.Sink.Names {
			ns, key, found := strings.Cut(sink, ":") // 形如 "secure:android_id"
			if !found {
				continue
			}
			// 已经是目标值就跳过（避免每次开机都写一遍）。
			if cur, _ := s.run.Run(ctx, "docker", "exec", name, "settings", "get", ns, key); strings.TrimSpace(cur) == val {
				wrote = true
				continue
			}
			if out, err := s.run.Run(ctx, "docker", "exec", name, "settings", "put", ns, key, val); err != nil {
				problems = append(problems, fmt.Sprintf("%s（%s）设置失败：%v %s", r.Key, sink, err, out))
				continue
			}
			wrote = true
		}
		if wrote {
			applied = append(applied, r.Key)
		}
	}

	if restart {
		// 语言变了：重启安卓框架（不是重启容器，几十秒）。
		if out, err := s.run.Run(ctx, "docker", "exec", name, "setprop", "ctl.restart", "zygote"); err != nil {
			problems = append(problems, fmt.Sprintf("重启安卓框架失败：%v %s", err, out))
		} else {
			// 框架重启期间设备算「开机中」，等它重新就绪再报完成（踩坑 #15）。
			s.boots.forget(id)
			time.Sleep(3 * s.pollEvery())
			if !s.waitBooted(ctx, id) {
				problems = append(problems, "安卓框架重启后没有恢复")
			}
		}
	}
	// 预装应用（Chrome 等）：放在语言重启之后，免得装到一半框架被重启。
	inst, probs := s.preinstall(ctx, id)
	for _, f := range inst {
		applied = append(applied, "预装 "+f)
	}
	problems = append(problems, probs...)
	status := SettingsStatus{State: "applied", Applied: applied}
	if len(problems) > 0 {
		status.State, status.Error = "failed", strings.Join(problems, "；")
	}
	s.settings.set(id, status)
}

type settingsTracker struct{ tracker[SettingsStatus] }

func (t *settingsTracker) set(id string, st SettingsStatus) {
	st.At = time.Now().UTC()
	t.put(id, st)
}
