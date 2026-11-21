package modem

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
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

// 安卓 16 模拟器电话 HAL（radio-service.ranchu）的开机初始化序列：每条都必须回 OK，否则它崩溃重启（踩坑 #121）。
// 插卡、没插卡两种都要过。
func TestRanchuInitSequence(t *testing.T) {
	seq := []string{"ATE0Q0V1", "AT+CMEE=1", "AT+CREG=2", "AT+CGREG=2", "AT+CEREG=2", "AT+CCWA=1", "AT+CMOD=0",
		"AT+CMUT=0", "AT+CSSN=0,1", "AT+COLP=0", `AT+CSCS="HEX"`, "AT+CUSD=1", "AT+CGEREP=1,0", "AT+CMGF=0", "AT+CFUN?"}
	for _, absent := range []bool{false, true} {
		m := New(Spec{IMEI: "351234567890123", Absent: absent}, nil)
		for _, c := range seq {
			if r := m.Handle(c); len(r) == 0 || r[len(r)-1] != "OK" {
				t.Fatalf("absent=%v %s 应回 OK：%v", absent, c, r)
			}
		}
	}
	// 真发 USSD 还是回错（没有这个功能）
	m := New(Spec{}, nil)
	if r := m.Handle(`AT+CUSD=1,"*100#",15`); r[len(r)-1] == "OK" {
		t.Fatalf("发 USSD 应回错：%v", r)
	}
}

// 安卓 16（ranchu）口味：IMEI 用 AT+CGSN=2 读（15+2 位裸数字）、运营商三连只用 \r 断行且名字不带引号、
// +CTEC 位掩码、开射频后主动上报注册和信号（踩坑 #121）。
func TestRanchuDialect(t *testing.T) {
	spec := Spec{IMEI: "351234567890123", IMSI: "460001234567890", MCC: "460", MNC: "00", Long: "China Mobile", Short: "CMCC",
		LAC: "1A2B", CellID: "00C0FFEE", Signal: 20, RSRP: 95, Dialect: DialectRanchu}
	in := strings.NewReader("AT+CGSN=2\rAT+COPS=3,0;+COPS?;+COPS=3,1;+COPS?;+COPS=3,2;+COPS?\rAT+CTEC?\rAT+CTEC=?\rAT+CFUN=1\r")
	var out bytes.Buffer
	m := New(spec, &out)
	_ = m.Serve(in)
	got := out.String()
	want := "35123456789012301\rOK\r" +
		"+COPS: 0,0,China Mobile\r+COPS: 0,1,CMCC\r+COPS: 0,2,46000\rOK\r" +
		"+CTEC: 32\rOK\r+CTEC: 0,1,5\rOK\r" +
		"OK\r+CREG: 1,\"1A2B\",\"00C0FFEE\",7\r+CGREG: 1,\"1A2B\",\"00C0FFEE\",7\r+CSQ: 20,99,-1,-1,-1,-1,-1,20,95,10,150,2147483647,2147483647\r"
	if got != want {
		t.Fatalf("ranchu 回复不对：\n得到 %q\n应为 %q", got, want)
	}
	if strings.Contains(got, "\n") {
		t.Fatal("ranchu 回复里不能有 \\n")
	}
	// 没插卡：查运营商回 CME 错误（不能回没有运营商的 +COPS，ranchu 会崩），开射频报「没注册」
	out.Reset()
	spec.Absent = true
	_ = New(spec, &out).Serve(strings.NewReader("AT+COPS=3,0;+COPS?\rAT+CFUN=1\r"))
	if got := out.String(); got != "+CME ERROR: 30\rOK\r+CREG: 0\r+CGREG: 0\r" && got != "+CME ERROR: 30\r+CREG: 0\r+CGREG: 0\r" && !strings.HasPrefix(got, "+CME ERROR: 30\r") {
		t.Fatalf("没插卡时：%q", got)
	}
}

// 老口味（安卓 13~15）不受影响：+COPS 仍带引号和制式、\r\n 断行、+CTEC 回错、开射频不主动上报。
func TestDefaultDialectUnchanged(t *testing.T) {
	var out bytes.Buffer
	_ = New(Spec{IMEI: "351234567890123", Long: "X", LAC: "1", CellID: "2"}, &out).Serve(strings.NewReader("AT+COPS?\rAT+CTEC?\rAT+CFUN=1\r"))
	want := "\r\n+COPS: 0,0,\"X\",7\r\n\r\nOK\r\n\r\nERROR\r\n\r\nOK\r\n"
	if out.String() != want {
		t.Fatalf("老口味变了：%q", out.String())
	}
}

// ranchu：开机会问 SIM 卡工具包 / CDMA 漫游，回 ERROR 它会卡死；定时报信号兼当解卡保险（踩坑 #121）。
func TestRanchuStkAndHeartbeat(t *testing.T) {
	m := New(Spec{Signal: 20, RSRP: 95, Dialect: DialectRanchu}, nil)
	for c, want := range map[string]string{"AT+CUSATD?": "+CUSATD: 0, 0", "AT+WRMP?": "+WRMP: 0"} {
		if r := m.Handle(c); len(r) != 2 || r[0] != want || r[1] != "OK" {
			t.Fatalf("%s：%v", c, r)
		}
	}
	old := HeartbeatEvery
	HeartbeatEvery = 20 * time.Millisecond
	defer func() { HeartbeatEvery = old }()
	pr, pw := io.Pipe()
	var mu sync.Mutex
	var out bytes.Buffer
	w := writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return out.Write(p) })
	m2 := New(Spec{Signal: 20, RSRP: 95, Dialect: DialectRanchu}, w)
	go func() { _ = m2.Serve(pr) }()
	time.Sleep(90 * time.Millisecond)
	pw.Close()
	mu.Lock()
	got := out.String()
	mu.Unlock()
	if strings.Count(got, "+CSQ: ") < 2 {
		t.Fatalf("应定时上报信号：%q", got)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
