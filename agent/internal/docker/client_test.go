package docker

import "testing"

func TestUsageFromStats(t *testing.T) {
	raw := []byte(`{"cpu_stats":{"cpu_usage":{"total_usage":3000000000},"system_cpu_usage":20000000000},
"precpu_stats":{"cpu_usage":{"total_usage":1000000000},"system_cpu_usage":10000000000},
"memory_stats":{"usage":1073741824,"limit":3221225472,"stats":{"inactive_file":268435456}}}`)
	u, err := UsageFromStats(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.CPUPercent != 20 || u.MemMB != 768 || u.MemLimitMB != 3072 {
		t.Fatalf("算错了：%+v", u)
	}
}
