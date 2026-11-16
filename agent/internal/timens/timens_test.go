package timens

import "testing"

func TestOffsets(t *testing.T) {
	got := Offsets(30, 9_600_000, 9_650_000)
	want := "monotonic -9599970 0\nboottime -9649970 0\n"
	if got != want {
		t.Fatalf("偏移不对：%q", got)
	}
}
