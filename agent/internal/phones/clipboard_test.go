package phones

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// clipRunner 假装手机里的剪贴板小程序：set 存下 base64，get 原样还回去；记下有没有按粘贴键。
type clipRunner struct {
	fakeRunner
	clip   string
	pasted int
	copied bool
}

func (r *clipRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	base, baseErr := r.fakeRunner.Run(ctx, name, args...) // 开机检查等照旧
	joined := strings.Join(args, " ")
	switch {
	case len(args) >= 1 && args[0] == "cp":
		r.copied = true
	case strings.Contains(joined, "KEYCODE_PASTE"):
		r.pasted++
	case strings.Contains(joined, ".sh set "):
		r.clip = args[len(args)-1]
		return "ok\n", nil
	case strings.HasSuffix(joined, ".sh get"):
		if r.clip == "" {
			return "empty\n", nil
		}
		return "ok " + r.clip + "\n", nil
	}
	return base, baseErr
}

func TestClipboard(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	ctx := context.Background()
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	if err := svc.SetClipboard(ctx, p.ID, "x", false); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("关机时应拒绝：%v", err)
	}
	if err := svc.Start(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	r := &clipRunner{}
	svc.run = r

	if got, err := svc.GetClipboard(ctx, p.ID); err != nil || got != "" {
		t.Fatalf("空剪贴板：%q %v", got, err)
	}
	text := "你好，云手机\n第二行 \"引号\" 'single' $HOME"
	if err := svc.SetClipboard(ctx, p.ID, text, false); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetClipboard(ctx, p.ID); err != nil || got != text {
		t.Fatalf("读回来不一样：%q %v", got, err)
	}
	if r.pasted != 0 {
		t.Fatal("只放进剪贴板时不该按粘贴")
	}
	// 发送中文：走剪贴板 + 粘贴键
	if err := svc.Input(ctx, p.ID, InputRequest{Kind: "text", Text: "张三 测试"}); err != nil {
		t.Fatal(err)
	}
	if r.pasted != 1 {
		t.Fatalf("发送中文应按一次粘贴键（%d）", r.pasted)
	}
	// 英文照旧走 input text，不动剪贴板
	before := r.clip
	if err := svc.Input(ctx, p.ID, InputRequest{Kind: "text", Text: "hello"}); err != nil || r.clip != before {
		t.Fatalf("英文不该动剪贴板：%v", err)
	}
	for _, bad := range []string{"", strings.Repeat("长", MaxClipboard)} {
		if err := svc.SetClipboard(ctx, p.ID, bad, false); !errors.Is(err, ErrInvalid) {
			t.Fatalf("应拒绝：%v", err)
		}
	}
	if !strings.HasPrefix(func() string { d, _ := clipPaths(); return d }(), "/data/local/tmp/pp-clip-") {
		t.Fatal("路径不对")
	}
}
