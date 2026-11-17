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
		// cuttlefish RIL 用多行方式发的查询必须带内容行，否则它读空指针崩溃（踩坑 #65）
		`AT+CLCK="FD",2,"",7`:   "+CLCK: 0|OK",
		`AT+CLCK="SC",2,""`:     "+CLCK: 0|OK",
		`AT+CLCK="SC",1,"1234"`: "OK",
		`AT+CPINR="SIM PIN2"`:   "+CPINR: SIM PIN2,3,3|OK",
		"AT+CICCID":             m.spec.ICCID + "|OK",
		"AT+CUSD=2":             "ERROR",
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

// 运营商表：每一家 MCC 3 位、MNC 2~3 位、国际区号是数字；生成的 IMSI 15 位、ICCID 19~20 位、开头对得上。
func TestCarriersWellFormed(t *testing.T) {
	isDigits := func(s string) bool {
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		return s != ""
	}
	for country, list := range carriers {
		for _, c := range list {
			if len(c.MCC) != 3 || len(c.MNC) < 2 || len(c.MNC) > 3 || !isDigits(c.MCC+c.MNC+c.ICCIDCountry) || c.Long == "" {
				t.Fatalf("%s %+v 格式不对", country, c)
			}
		}
		for seed := int64(0); seed < 20; seed++ {
			s := Pick(country, "", seed)
			if s.Absent || len(s.IMSI) != 15 || !strings.HasPrefix(s.IMSI, s.MCC+s.MNC) || len(s.IMEI) != 15 || len(s.ICCID) < 19 {
				t.Fatalf("%s：%+v", country, s)
			}
		}
	}
	for _, c := range []string{"MX", "KR", "FR", "TH", "BR", "AU"} {
		if carriers[c] == nil {
			t.Fatalf("热门国家 %s 没有运营商", c)
		}
	}
}

// 不在表里的国家：没插卡（不能随便塞香港的卡）
func TestAbsentSIM(t *testing.T) {
	s := Pick("ZZ", "", 1)
	if !s.Absent || s.IMSI != "" || len(s.IMEI) != 15 {
		t.Fatalf("%+v", s)
	}
	if s2 := Pick("", "", 1); !s2.Absent {
		t.Fatal("代理还没检测出国家时也当没插卡")
	}
	var out strings.Builder
	m := New(s, &out)
	cases := map[string]string{
		"AT+CPIN?":          "+CME ERROR: 10",
		"AT+CIMI":           "+CME ERROR: 10",
		`AT+CLCK="SC",2,""`: "+CME ERROR: 10",
		"AT+CREG?":          "+CREG: 2,0|OK",
		"AT+COPS?":          "+COPS: 0|OK",
		"AT+CGSN":           s.IMEI + "|OK",
		"AT+CFUN?":          "+CFUN: 1|OK",
	}
	for cmd, want := range cases {
		if got := strings.Join(m.Handle(cmd), "|"); got != want {
			t.Fatalf("%s → %q，想要 %q", cmd, got, want)
		}
	}
}
