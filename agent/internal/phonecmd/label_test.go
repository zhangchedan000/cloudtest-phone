package phonecmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookup(t *testing.T) {
	d := t.TempDir()
	plat := filepath.Join(d, "plat")
	vend := filepath.Join(d, "vend")
	os.WriteFile(plat, []byte(`
/system(/.*)?           u:object_r:system_file:s0
/system/bin/init        u:object_r:init_exec:s0
/system/bin/sh          -- u:object_r:shell_exec:s0
/dev(/.*)?              u:object_r:device:s0
/dev/binder             u:object_r:binder_device:s0
/dev/socket(/.*)?       u:object_r:socket_device:s0
/system/etc/hosts       u:object_r:system_file:s0
/data/nolabel(/.*)?     <<none>>
`), 0o644)
	os.WriteFile(vend, []byte(`
/vendor(/.*)?                     u:object_r:vendor_file:s0
/(vendor|system/vendor)/bin/hw/android\.hardware\.media\.c2@1\.1-service   u:object_r:mediacodec_exec:s0
/system/bin/[a-z]+d               u:object_r:daemon_exec:s0
`), 0o644)
	l, _ := LoadLabeler([]string{plat, vend})
	cases := []struct {
		p    string
		m    os.FileMode
		want string
	}{
		{"/system/bin/init", 0, "u:object_r:init_exec:s0"},              // 纯路径条目优先于后面的正则
		{"/system/bin/logd", 0, "u:object_r:daemon_exec:s0"},            // 后面的正则胜过前面的正则
		{"/system/bin/sh", 0, "u:object_r:shell_exec:s0"},               // 类型对
		{"/system/bin/sh", os.ModeSymlink, "u:object_r:system_file:s0"}, // 类型不对就往前找
		{"/vendor/bin/hw/android.hardware.media.c2@1.1-service", 0, "u:object_r:mediacodec_exec:s0"},
		{"/dev/binder", os.ModeCharDevice | os.ModeDevice, "u:object_r:binder_device:s0"},
		{"/dev/socket/zygote", os.ModeSocket, "u:object_r:socket_device:s0"},
		{"/dev/urandom", os.ModeCharDevice | os.ModeDevice, "u:object_r:device:s0"},
		{"/data/nolabel/x", 0, ""},
		{"/nowhere", 0, ""},
	}
	for _, c := range cases {
		if got := l.Lookup(c.p, c.m); got != c.want {
			t.Errorf("%s (%v)：得到 %q，应为 %q", c.p, c.m, got, c.want)
		}
	}
}
