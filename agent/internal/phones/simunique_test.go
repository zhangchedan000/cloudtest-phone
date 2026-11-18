package phones

import (
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
		if base := simPick(*p, snap, byKey, 0); id == minID(ids) && base.IMEI != sp.IMEI {
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
