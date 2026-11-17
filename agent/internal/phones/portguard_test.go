package phones

import (
	"strings"
	"testing"
)

func TestPortGuardRule(t *testing.T) {
	r := strings.Join(portGuardRule(), " ")
	for _, want := range []string{"-o lo", "--dports 6401:6404,9600", "--uid-owner 10000-2147483647", "REJECT --reject-with tcp-reset"} {
		if !strings.Contains(r, want) {
			t.Fatalf("规则里没有 %q：%s", want, r)
		}
	}
}
