package modem

import (
	"fmt"
	"math/rand"
	"strings"
)

// Carrier 一家运营商（MCC / MNC 是国际电联分配的真实号段）。
type Carrier struct {
	Long, Short, SPN string
	MCC, MNC         string
	ICCIDCountry     string // ICCID 里的国家码（89 + 国际电话区号）
}

// carriers 各国 / 地区主要运营商（按代理出口国家选；同一台手机每次选到同一家）。
var carriers = map[string][]Carrier{
	"HK": {
		{"csl.", "csl.", "csl.", "454", "00", "852"},
		{"SmarTone", "SmarTone", "SmarTone", "454", "06", "852"},
		{"3", "3", "3", "454", "03", "852"},
		{"China Mobile HK", "CMHK", "CMHK", "454", "12", "852"},
	},
	"US": {
		{"T-Mobile", "T-Mobile", "T-Mobile", "310", "260", "1"},
		{"AT&T", "AT&T", "AT&T", "310", "410", "1"},
		{"Verizon", "Verizon", "Verizon Wireless", "311", "480", "1"},
	},
	"SG": {
		{"SingTel", "SingTel", "SingTel", "525", "01", "65"},
		{"StarHub", "StarHub", "StarHub", "525", "05", "65"},
		{"M1", "M1", "M1", "525", "03", "65"},
	},
	"JP": {
		{"NTT DOCOMO", "docomo", "NTT DOCOMO", "440", "10", "81"},
		{"au", "au", "au", "440", "50", "81"},
		{"SoftBank", "SoftBank", "SoftBank", "440", "20", "81"},
	},
	"TW": {
		{"Chunghwa Telecom", "CHT", "中華電信", "466", "92", "886"},
		{"FarEasTone", "FET", "遠傳電信", "466", "01", "886"},
		{"Taiwan Mobile", "TWM", "台灣大哥大", "466", "97", "886"},
	},
	"GB": {
		{"EE", "EE", "EE", "234", "30", "44"},
		{"vodafone UK", "vodafone", "vodafone UK", "234", "15", "44"},
		{"O2 - UK", "O2", "O2 - UK", "234", "10", "44"},
	},
	"DE": {
		{"Telekom.de", "Telekom", "Telekom.de", "262", "01", "49"},
		{"Vodafone.de", "Vodafone", "Vodafone.de", "262", "02", "49"},
		{"o2 - de", "o2", "o2 - de", "262", "03", "49"},
	},
	"MY": {
		{"Maxis", "Maxis", "Maxis", "502", "12", "60"},
		{"CelcomDigi", "CelcomDigi", "CelcomDigi", "502", "13", "60"},
	},
}

// Pick 按国家选一家运营商、生成这张 SIM 卡和基带的身份。seed 相同结果相同（一键新机换 seed 就换卡）。
// tac 是机型的 IMEI 前 8 位（型号核准码），空就用一个通用的。
func Pick(country, tac string, seed int64) Spec {
	r := rand.New(rand.NewSource(seed))
	list := carriers[strings.ToUpper(country)]
	if len(list) == 0 {
		list = carriers["HK"]
	}
	c := list[r.Intn(len(list))]
	digits := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteByte(byte('0' + r.Intn(10)))
		}
		return b.String()
	}
	if len(tac) != 8 {
		tac = "35" + digits(6)
	}
	imei := tac + digits(6)
	imsi := c.MCC + c.MNC + digits(15-len(c.MCC)-len(c.MNC))
	iccid := "89" + c.ICCIDCountry + c.MNC + digits(19-2-len(c.ICCIDCountry)-len(c.MNC)-1)
	return Spec{
		IMEI: imei + luhn(imei), IMSI: imsi, ICCID: iccid + luhn(iccid),
		MCC: c.MCC, MNC: c.MNC, Long: c.Long, Short: c.Short, SPN: c.SPN,
		LAC: fmt.Sprintf("%04X", 0x1000+r.Intn(0xE000)), CellID: fmt.Sprintf("%08X", 0x01000000+r.Intn(0x0F000000)),
		Signal: 16 + r.Intn(10), RSRP: 85 + r.Intn(20),
		Version: "MOLY.NR17.R1.TC8.PR1.SP.V1.P" + fmt.Sprint(10+r.Intn(40)),
	}
}

// luhn 算校验位（IMEI、ICCID 最后一位）。
func luhn(s string) string {
	sum := 0
	for i := len(s) - 1; i >= 0; i-- {
		d := int(s[i] - '0')
		if (len(s)-1-i)%2 == 0 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return fmt.Sprint((10 - sum%10) % 10)
}
