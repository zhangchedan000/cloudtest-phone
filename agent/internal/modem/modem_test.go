package modem

import (
	"strings"
	"testing"
)

func validLuhn(s string) bool { return luhn(s[:len(s)-1]) == s[len(s)-1:] }

func TestPick(t *testing.T) {
	s := Pick("hk", "86812345", 42)
	if len(s.IMEI) != 15 || !strings.HasPrefix(s.IMEI, "86812345") || !validLuhn(s.IMEI) {
		t.Fatalf("IMEI %q", s.IMEI)
	}
	if len(s.IMSI) != 15 || !strings.HasPrefix(s.IMSI, s.MCC+s.MNC) || s.MCC != "454" {
		t.Fatalf("IMSI %q", s.IMSI)
	}
	if len(s.ICCID) != 19 || !strings.HasPrefix(s.ICCID, "89852") || !validLuhn(s.ICCID) {
		t.Fatalf("ICCID %q", s.ICCID)
	}
	if Pick("HK", "86812345", 42) != s {
		t.Fatal("同一个 seed 应该一样")
	}
	if us := Pick("US", "", 1); us.MCC[0] != '3' || len(us.MNC) != 3 {
		t.Fatalf("美国 %+v", us)
	}
}

func TestATConversation(t *testing.T) {
	var out strings.Builder
	m := New(Pick("HK", "", 7), &out)
	cases := map[string]string{
		"ATE0Q0V1":    "OK",
		"AT+CPIN?":    "+CPIN: READY|OK",
		"AT+CFUN?":    "+CFUN: 1|OK",
		"AT+CGSN":     m.spec.IMEI + "|OK",
		"AT+CIMI":     m.spec.IMSI + "|OK",
		"AT+CTEC?":    "ERROR",
		"ATD*99***1#": "ERROR",
		"AT+CMEE=1":   "OK",
	}
	for cmd, want := range cases {
		if got := strings.Join(m.Handle(cmd), "|"); got != want {
			t.Fatalf("%s → %q，想要 %q", cmd, got, want)
		}
	}
	got := strings.Join(m.Handle("AT+COPS=3,0;+COPS?;+COPS=3,1;+COPS?;+COPS=3,2;+COPS?"), "|")
	want := `+COPS: 0,0,"` + m.spec.Long + `",7|+COPS: 0,1,"` + m.spec.Short + `",7|+COPS: 0,2,"` + m.spec.MCC + m.spec.MNC + `",7|OK`
	if got != want {
		t.Fatalf("运营商名：\n%s\n%s", got, want)
	}
	if csq := m.Handle("AT+CSQ")[0]; strings.Count(csq, ",") < 11 {
		t.Fatalf("信号至少 12 个数：%s", csq)
	}
	if reg := m.Handle("AT+CREG?")[0]; !strings.HasPrefix(reg, "+CREG: 2,1,") {
		t.Fatal(reg)
	}
}

func TestSimIO(t *testing.T) {
	m := New(Spec{ICCID: "8985200123456789012", MNC: "00", SPN: "csl."}, nil)
	resp := m.Handle("AT+CRSM=192,12258,0,0,15")[0] // GET RESPONSE EF_ICCID
	if !strings.HasPrefix(resp, "+CRSM: 144,0,0000000A2FE204") {
		t.Fatalf("GET RESPONSE：%s", resp)
	}
	if got := m.Handle("AT+CRSM=176,12258,0,0,10")[0]; got != "+CRSM: 144,0,985802103254769810F2" {
		t.Fatalf("ICCID：%s", got)
	}
	if got := m.Handle("AT+CRSM=176,28589,0,0,4")[0]; got != "+CRSM: 144,0,00000002" {
		t.Fatalf("EF_AD：%s", got)
	}
	if got := m.Handle("AT+CRSM=176,28486,0,0,17")[0]; !strings.HasPrefix(got, "+CRSM: 144,0,0163736C2E") {
		t.Fatalf("SPN：%s", got)
	}
	if got := m.Handle("AT+CRSM=176,28480,0,0,0")[0]; got != "+CRSM: 106,130" {
		t.Fatalf("没有的文件：%s", got)
	}
}
