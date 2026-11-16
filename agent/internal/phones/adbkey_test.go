package phones

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func testADBKey() string {
	return base64.StdEncoding.EncodeToString(make([]byte, 524)) + " me@pc"
}

func TestCheckADBKey(t *testing.T) {
	if _, err := checkADBKey("  " + testADBKey() + "\r\n"); err != nil {
		t.Fatalf("合法公钥被拒：%v", err)
	}
	for _, bad := range []string{"", "abc", base64.StdEncoding.EncodeToString(make([]byte, 100)), testADBKey() + "; rm -rf /", testADBKey() + "\nsecond"} {
		if _, err := checkADBKey(bad); err == nil {
			t.Errorf("应拒绝：%q", bad)
		}
	}
}

func TestAuthorizeADBKey(t *testing.T) {
	svc, id := startForResume(t)
	r := &fakeRunner{}
	svc.run = r
	if err := svc.AuthorizeADBKey(context.Background(), id, testADBKey()); err != nil {
		t.Fatal(err)
	}
	last := r.calls[len(r.calls)-1]
	if !strings.Contains(last, adbKeysPath) || !strings.HasSuffix(last, testADBKey()) {
		t.Fatalf("命令不对：%s", last)
	}
}
