package phones

import (
	"strings"
	"testing"
)

func TestValidMAC(t *testing.T) {
	for _, ok := range []string{"3c:5a:b4:01:02:03", "AA:BB:CC:DD:EE:FF"} {
		if !validMAC(ok) {
			t.Fatal(ok)
		}
	}
	for _, bad := range []string{"", "3c:5a:b4:01:02", "3c-5a-b4-01-02-03", "3c:5a:b4:01:02:0g", "3c:5a:b4:01:02:03:"} {
		if validMAC(bad) {
			t.Fatal(bad)
		}
	}
}

func TestRootcanalArgs(t *testing.T) {
	got := strings.Join(rootcanalArgs(1234), " ")
	if !strings.HasPrefix(got, "nsenter -t 1234 -n "+RootcanalBin) || !strings.Contains(got, "--hci_port=6402") {
		t.Fatal(got)
	}
}
