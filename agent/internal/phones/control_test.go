package phones

import (
	"context"
	"errors"
	"github.com/zhangchedan000/prism-phone/agent/internal/docker"
	"strings"
	"testing"
)

type fakeRaw struct{ out []byte }

func (f fakeRaw) Output(context.Context, string, ...string) ([]byte, error) { return f.out, nil }

func TestInputArgs(t *testing.T) {
	cases := []struct {
		req  InputRequest
		want string
	}{
		{InputRequest{Kind: "tap", X: 10, Y: 20}, "tap 10 20"},
		{InputRequest{Kind: "swipe", X: 1, Y: 2, X2: 3, Y2: 4}, "swipe 1 2 3 4 300"},
		{InputRequest{Kind: "swipe", X: 1, Y: 2, X2: 3, Y2: 4, Duration: 99999}, "swipe 1 2 3 4 5000"},
		{InputRequest{Kind: "text", Text: "hi there's"}, `text 'hi%sthere'\''s'`},
		{InputRequest{Kind: "key", Key: "back"}, "keyevent KEYCODE_BACK"},
		{InputRequest{Kind: "key", Key: "tab"}, "keyevent KEYCODE_TAB"},
		{InputRequest{Kind: "key", Key: "left"}, "keyevent KEYCODE_DPAD_LEFT"},
		{InputRequest{Kind: "key", Key: "select_all"}, "keycombination KEYCODE_CTRL_LEFT KEYCODE_A"},
		{InputRequest{Kind: "key", Key: "copy"}, "keycombination KEYCODE_CTRL_LEFT KEYCODE_C"},
	}
	for _, c := range cases {
		got, err := inputArgs(c.req)
		if err != nil || strings.Join(got, " ") != c.want {
			t.Fatalf("%+v → %q %v，想要 %q", c.req, strings.Join(got, " "), err, c.want)
		}
	}
	for _, bad := range []InputRequest{{Kind: "tap", X: -1}, {Kind: "text", Text: "中文"}, {Kind: "text"}, {Kind: "key", Key: "x"}, {Kind: "rm"}} {
		if _, err := inputArgs(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v 应该被拒绝，得到 %v", bad, err)
		}
	}
}

func TestScreenshotAndInputNeedARunningPhone(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, err := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc.raw = fakeRaw{out: []byte("\x89PNG\r\n\x1a\nxx")}
	if _, err := svc.Screenshot(ctx, p.ID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("关机时截屏应该报没开机：%v", err)
	}
	if err := svc.Start(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	png, err := svc.Screenshot(ctx, p.ID)
	if err != nil || string(png[1:4]) != "PNG" {
		t.Fatalf("截屏失败：%v", err)
	}
	if err := svc.Input(ctx, p.ID, InputRequest{Kind: "tap", X: 5, Y: 6}); err != nil {
		t.Fatal(err)
	}
	r := svc.run.(*fakeRunner)
	if last := r.calls[len(r.calls)-1]; !strings.HasSuffix(last, "sh -c input tap 5 6") {
		t.Fatalf("命令不对：%s", last)
	}
	svc.raw = fakeRaw{out: []byte("error: not ready")}
	if _, err := svc.Screenshot(ctx, p.ID); err == nil {
		t.Fatal("拿不到图片应该报错")
	}
}

// notBooted 假装安卓还没开机完成。
type notBooted struct{ fakeRunner }

func (r *notBooted) Run(ctx context.Context, name string, args ...string) (string, error) {
	if len(args) >= 5 && args[2] == "sh" && args[4] == readyProbe {
		return "1\nService input: not found", nil
	}
	return r.fakeRunner.Run(ctx, name, args...)
}

func TestStartingUntilAndroidBooted(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, err := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := svc.Start(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	svc.run = &notBooted{}
	if got := svc.State(ctx, p.ID); got != "starting" {
		t.Fatalf("没开机完成应是 starting，得到 %s", got)
	}
	svc.raw = fakeRaw{out: []byte("\x89PNG\r\n\x1a\nxx")}
	if _, err := svc.Screenshot(ctx, p.ID); err != nil {
		t.Fatalf("开机中也能截屏（看开机画面）：%v", err)
	}
	if err := svc.Input(ctx, p.ID, InputRequest{Kind: "key", Key: "home"}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("开机中操作应提示等开机：%v", err)
	}
	svc.run = &fakeRunner{}
	if got := svc.State(ctx, p.ID); got != "running" {
		t.Fatalf("开机完成应是 running，得到 %s", got)
	}
}

func TestNetworkReasonShownInList(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	svc.SetNetworkGuard(func(id string) error {
		if id == p.ID {
			return errors.New("没有网络出口：没有分配代理")
		}
		return nil
	})
	views := svc.List(context.Background())
	if len(views) != 1 || !strings.Contains(views[0].Network, "没有分配代理") {
		t.Fatalf("列表里应显示网络原因：%+v", views)
	}
	if err := svc.Start(context.Background(), p.ID); !errors.Is(err, ErrBlocked) {
		t.Fatalf("没有出口应禁止开机：%v", err)
	}
}

func TestLogs(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	ctx := context.Background()
	if _, err := svc.Logs(ctx, p.ID, 10, "E"); !errors.Is(err, ErrNotRunning) {
		t.Fatal("关机时应拒绝")
	}
	_ = svc.Start(ctx, p.ID)
	if _, err := svc.Logs(ctx, p.ID, 99999, "X; reboot"); err != nil {
		t.Fatal(err)
	}
	r := svc.run.(*fakeRunner)
	if last := r.calls[len(r.calls)-1]; !strings.HasSuffix(last, "logcat -d -t 300 *:W") {
		t.Fatalf("参数应被纠正为默认值：%s", last)
	}
}

func TestReadyOutput(t *testing.T) {
	for out, want := range map[string]bool{
		"1\nService input: found":     true,
		"1\nService input: not found": false,
		"\nService input: found":      false,
		"1":                           false,
	} {
		if readyOutput(out) != want {
			t.Fatalf("%q 应为 %v", out, want)
		}
	}
}

func TestStreamArgs(t *testing.T) {
	got := strings.Join(streamArgs(StreamOptions{Width: 541, Height: 1230, BitRate: 1_000_000}), " ")
	if got != "screenrecord --output-format=h264 --time-limit 180 --size 528x1216 --bit-rate 1000000 -" {
		t.Fatalf("参数不对：%s", got)
	}
	if got := strings.Join(streamArgs(StreamOptions{Width: 99999, BitRate: -1}), " "); got != "screenrecord --output-format=h264 --time-limit 180 --bit-rate 2000000 -" {
		t.Fatalf("不合理的参数应用默认值：%s", got)
	}
}

type usageDocker struct{ *fakeDocker }

func (usageDocker) ContainerUsage(context.Context, string) (docker.Usage, error) {
	return docker.Usage{CPUPercent: 12.5, MemMB: 900, MemLimitMB: 3072}, nil
}

func TestUsageShownForRunningPhones(t *testing.T) {
	svc, fd, _ := newTestService(t, true, "software")
	svc.docker = usageDocker{fd}
	a, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	b, _ := svc.Create(CreateRequest{Name: "B", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	ctx := context.Background()
	_ = svc.Start(ctx, a.ID)
	svc.SampleUsage(ctx)
	for _, v := range svc.List(ctx) {
		if v.ID == a.ID && (v.Usage == nil || v.Usage.MemMB != 900) {
			t.Fatalf("运行中的设备应有占用：%+v", v.Usage)
		}
		if v.ID == b.ID && v.Usage != nil {
			t.Fatalf("关机的设备不应有占用：%+v", v.Usage)
		}
	}
}
