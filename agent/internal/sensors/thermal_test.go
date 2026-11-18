package sensors

import "testing"

func TestThermalZonesFor(t *testing.T) {
	z1 := ThermalZonesFor(111, 34400) // 基准 34.4℃
	z2 := ThermalZonesFor(222, 34400)
	if len(z1) < 8 {
		t.Fatalf("热区太少：%d", len(z1))
	}
	// 同种子稳定
	if a, b := ThermalZonesFor(111, 34400), z1; a[0] != b[0] {
		t.Fatalf("同种子不稳定")
	}
	// 不同种子温度不同（至少第一个区）
	if z1[0].MilliC == z2[0].MilliC {
		t.Fatalf("不同种子温度一样了：%d", z1[0].MilliC)
	}
	// 类型非空、温度在合理范围（20~60℃）
	for _, z := range z1 {
		if z.Type == "" || z.MilliC < 20000 || z.MilliC > 60000 {
			t.Fatalf("热区不合理：%+v", z)
		}
	}
}
