package phones

import "testing"

func TestKernelMajorMinor(t *testing.T) {
	cases := map[string]string{
		"5.10.187-android12-9": "5.10",
		"5.15.142-android13-8": "5.15",
		"6.1.0":                "6.1",
		"":                     "",
		"garbage":              "",
		"5":                    "",
		"5.x.1":                "",
	}
	for in, want := range cases {
		if got := kernelMajorMinor(in); got != want {
			t.Fatalf("kernelMajorMinor(%q)=%q 要 %q", in, got, want)
		}
	}
}
