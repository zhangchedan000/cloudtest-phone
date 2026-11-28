package phones

import (
	"context"
	"testing"
)

// 两个窗口看同一台：新一路顶掉旧一路后，旧一路结束时不能把新一路当成自己的去清理。
func TestStreamTrackerFinishOnlyCurrent(t *testing.T) {
	var tr streamTracker
	ctx1, cancel1 := context.WithCancel(context.Background())
	g1, n1 := tr.replace("p", cancel1)
	_, cancel2 := context.WithCancel(context.Background())
	g2, n2 := tr.replace("p", cancel2)
	if ctx1.Err() == nil {
		t.Fatal("新一路应顶掉旧一路")
	}
	if n1 != 1 || n2 != 1 {
		t.Fatalf("同一台顶替后总路数应始终为 1，得到 %d / %d", n1, n2)
	}
	if tr.finish("p", g1) {
		t.Fatal("被顶掉的旧一路不该清理新一路")
	}
	if !tr.finish("p", g2) {
		t.Fatal("当前那一路结束时应清理")
	}
	if tr.finish("p", g2) {
		t.Fatal("清理过一次就不该再清理")
	}
}
