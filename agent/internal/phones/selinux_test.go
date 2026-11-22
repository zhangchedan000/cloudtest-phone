package phones

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteExec(t *testing.T) {
	d := t.TempDir()
	s := &Service{cfg: Config{DataDir: d, SELinux: "enforcing", InitHelper: "/opt/agent"}}
	// 没有套接字：原样
	if _, _, ok := s.rewriteExec("docker", []string{"exec", "pp-abc", "settings", "get", "x"}); ok {
		t.Fatal("没有小助手时不该改写")
	}
	_ = os.MkdirAll(s.cmdDir("abc"), 0o700)
	l, err := net.Listen("unix", filepath.Join(s.cmdDir("abc"), "s"))
	if err != nil {
		t.Skip("建不了 unix 套接字：", err)
	}
	defer l.Close()
	n, a, ok := s.rewriteExec("docker", []string{"exec", "-e", "PP_CLIP_DEX=/x.dex", "pp-abc", "sh", "/x.sh", "hi"})
	want := "/opt/agent pp-cmdc " + filepath.Join(d, "phones", "abc", "ppc", "s") + " -e PP_CLIP_DEX=/x.dex -- sh /x.sh hi"
	if !ok || n+" "+strings.Join(a, " ") != want {
		t.Fatalf("得到 %v %q\n应为 %q", ok, n+" "+strings.Join(a, " "), want)
	}
	if _, _, ok := s.rewriteExec("docker", []string{"inspect", "pp-abc"}); ok {
		t.Fatal("不是 exec 不该改写")
	}
	if c, p := s.cpTarget("docker", []string{"cp", "/tmp/a.apk", "pp-abc:/data/local/tmp/a.apk"}); c != "pp-abc" || p != "/data/local/tmp/a.apk" {
		t.Fatalf("cp 目标：%q %q", c, p)
	}
	s.cfg.SELinux = ""
	if _, _, ok := s.rewriteExec("docker", []string{"exec", "pp-abc", "ls"}); ok {
		t.Fatal("没开 -selinux 不该改写")
	}
}
