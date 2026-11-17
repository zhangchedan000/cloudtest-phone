package sensors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupPowerSupply(t *testing.T) {
	PowerSupplyDir = t.TempDir()
	if err := SetupPowerSupply(Battery{Level: 63, TempDeci: 318, Charging: "usb"}); err != nil {
		t.Fatal(err)
	}
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join(PowerSupplyDir, p))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	for p, want := range map[string]string{
		"battery/type": "Battery", "battery/technology": "Li-poly", "battery/capacity": "63", "battery/temp": "318",
		"battery/status": "Charging", "battery/health": "Good", "battery/present": "1", "usb/online": "1", "ac/online": "0", "ac/type": "Mains",
	} {
		if got := read(p); got != want {
			t.Fatalf("%s = %q，想要 %q", p, got, want)
		}
	}
	if BatteryStatus(100, "ac") != "Full" || BatteryStatus(40, "none") != "Discharging" {
		t.Fatal("status 不对")
	}
}
