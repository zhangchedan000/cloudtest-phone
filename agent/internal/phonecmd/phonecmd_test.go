package phonecmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d", "s")
	defaultEnv = []string{"PATH=/usr/bin:/bin"}
	go func() { _ = Serve(sock) }()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var out, errb bytes.Buffer
	code, err := Run(sock, []string{"PP_X=42"}, []string{"sh", "-c", "echo hi $PP_X; echo oops >&2; exit 3"}, nil, &out, &errb)
	if err != nil || code != 3 || strings.TrimSpace(out.String()) != "hi 42" || strings.TrimSpace(errb.String()) != "oops" {
		t.Fatalf("code=%d err=%v out=%q errb=%q", code, err, out.String(), errb.String())
	}
	out.Reset()
	code, err = Run(sock, nil, []string{"cat"}, strings.NewReader("abc\n"), &out, &errb)
	if err != nil || code != 0 || out.String() != "abc\n" {
		t.Fatalf("stdin: code=%d err=%v out=%q", code, err, out.String())
	}
	out.Reset()
	big := strings.Repeat("x", 200000)
	code, err = Run(sock, nil, []string{"sh", "-c", "head -c 200000 /dev/zero | tr '\\0' x"}, nil, &out, &errb)
	if err != nil || code != 0 || out.String() != big {
		t.Fatalf("big: code=%d err=%v len=%d", code, err, out.Len())
	}
	code, _ = Run(sock, nil, []string{"no-such-cmd-xyz"}, nil, &out, &errb)
	if code != 127 {
		t.Fatalf("missing command code=%d", code)
	}
}
