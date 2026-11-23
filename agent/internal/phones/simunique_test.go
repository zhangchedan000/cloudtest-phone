package phones

import (
	"fmt"
	"testing"

	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

// 两台手机种子一样（同一个 androidId）也不能标识重复：后建的换一个号；先建的号码不变（老手机号码不受影响）。
func TestSIMNoCollision(t *testing.T) {
	svc, _, st := newTestService(t, true, "software")
	addProxy(t, st, "us", true)
	var ids []string
	for _, n := range []string{"A", "B", "C"} {
		p, err := svc.Create(CreateRequest{Name: n, ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: "us"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	// 三台的 androidId 改成同一个 → 原来的号会一模一样
	_ = st.Update(func(s *store.State) error {
		for i := range s.Phones {
			s.Phones[i].Fields["androidId"] = store.FieldSetting{Mode: "manual", Value: "0123456789abcdef"}
		}
		return nil
	})
	snap := st.Snapshot()
	seen := map[string]string{}
	for _, id := range ids {
		p, _ := snap.Phone(id)
		byKey := map[string]any{}
		for _, r := range svc.resolve(*p, snap) {
			byKey[r.Key] = r.Value
		}
		sp := svc.simSpec(*p, snap, byKey)
		for _, v := range []string{sp.IMEI, sp.IMSI, sp.ICCID} {
			if other, dup := seen[v]; dup {
				t.Fatalf("%s 和 %s 标识重复：%s", id, other, v)
			}
			seen[v] = id
		}
		if base := simPick(*p, snap, byKey, 0, svc.modelTacs(p.ModelID)); id == minID(ids) && base.IMEI != sp.IMEI {
			t.Fatal("最先建的那台号码不该变")
		}
	}
}

func minID(ids []string) string {
	m := ids[0]
	for _, id := range ids {
		if id < m {
			m = id
		}
	}
	return m
}

// IMEI 前 8 位用机型档案里的真实 TAC（不是随机 35xxxxxx），同一台手机每次一样。
func TestImeiUsesModelTac(t *testing.T) {
	svc, _, st := newTestService(t, true, "software")
	addProxy(t, st, "us", true)
	tacs := svc.modelTacs("xiaomi-13t")
	if len(tacs) == 0 {
		t.Fatal("xiaomi-13t 档案里没有 imeiTacs")
	}
	seen := map[string]bool{}
	for i := 0; i < 12; i++ {
		p, err := svc.Create(CreateRequest{Name: fmt.Sprint("t", i), ModelID: "xiaomi-13t", AndroidVersion: 14, ProxyID: "us"})
		if err != nil {
			t.Fatal(err)
		}
		snap := st.Snapshot()
		byKey := map[string]any{}
		for _, r := range svc.resolve(p, snap) {
			byKey[r.Key] = r.Value
		}
		a := simPick(p, snap, byKey, 0, tacs)
		b := simPick(p, snap, byKey, 3, tacs)
		ok := false
		for _, tac := range tacs {
			ok = ok || a.IMEI[:8] == tac
		}
		if !ok || a.IMEI[:8] != b.IMEI[:8] || len(a.IMEI) != 15 {
			t.Fatalf("IMEI %s / %s：前 8 位要是机型 TAC 之一、换种子不变", a.IMEI, b.IMEI)
		}
		seen[a.IMEI[:8]] = true
	}
	if len(seen) < 2 {
		t.Fatalf("12 台只用了 %d 个 TAC，应该分散开", len(seen))
	}
}
