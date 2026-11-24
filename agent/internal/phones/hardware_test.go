package phones

import "testing"

func TestPickHardware(t *testing.T) {
	have := []string{"mt6886", "mt6895", "mt6896"}
	cases := []struct {
		vals map[string]any
		want string
	}{
		{map[string]any{"socModel": "MT6896"}, "mt6896"},
		{map[string]any{"socModel": "MT6895Z/TCZA"}, "mt6895"}, // vivo 真机写法带后缀
		{map[string]any{"socModel": "MT6896Z/CZA"}, "mt6896"},
		{map[string]any{"socModel": "SM8550", "boardPlatform": "mt6895"}, "mt6895"},
		{map[string]any{"socModel": "SM8550"}, "mt6886"}, // 都对不上：用镜像里有的，不退回 redroid
	}
	for _, c := range cases {
		if got := pickHardware(have, c.vals); got != c.want {
			t.Errorf("%v → %q，应该是 %q", c.vals, got, c.want)
		}
	}
	if got := pickHardware(nil, map[string]any{"socModel": "MT6895"}); got != "" {
		t.Errorf("老镜像（没有 prism.hw 标签）应该返回空，得到 %q", got)
	}
}
