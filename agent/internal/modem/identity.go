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
	// 2026-11-17 补热门国家（用户：主要就是几个热门国家，都要做）。MCC/MNC 是各家公开的真实值；
	// 美洲常见 3 位 MNC（墨西哥、加拿大），EF_AD 里按位数写（sim.go）。
	"KR": {
		{"SK Telecom", "SKTelecom", "SKTelecom", "450", "05", "82"},
		{"KT", "olleh", "KT", "450", "08", "82"},
		{"LG U+", "LG U+", "LG U+", "450", "06", "82"},
	},
	"MX": {
		{"TELCEL", "TELCEL", "TELCEL", "334", "020", "52"},
		{"AT&T MX", "AT&T", "AT&T", "334", "090", "52"},
		{"Movistar", "Movistar", "Movistar", "334", "030", "52"},
	},
	"CA": {
		{"Rogers Wireless", "Rogers", "Rogers", "302", "720", "1"},
		{"Bell", "Bell", "Bell", "302", "610", "1"},
		{"TELUS", "TELUS", "TELUS", "302", "220", "1"},
	},
	"AU": {
		{"Telstra", "Telstra", "Telstra", "505", "01", "61"},
		{"Optus", "Optus", "Optus", "505", "02", "61"},
		{"vodafone AU", "vodafone", "vodafone AU", "505", "03", "61"},
	},
	"NZ": {
		{"One NZ", "One NZ", "One NZ", "530", "01", "64"},
		{"Spark NZ", "Spark", "Spark NZ", "530", "05", "64"},
		{"2degrees", "2degrees", "2degrees", "530", "24", "64"},
	},
	"FR": {
		{"Orange F", "Orange", "Orange F", "208", "01", "33"},
		{"SFR", "SFR", "SFR", "208", "10", "33"},
		{"Bouygues Telecom", "BYTEL", "Bouygues Telecom", "208", "20", "33"},
		{"Free", "Free", "Free", "208", "15", "33"},
	},
	"IT": {
		{"TIM", "TIM", "TIM", "222", "01", "39"},
		{"vodafone IT", "vodafone", "vodafone IT", "222", "10", "39"},
		{"WINDTRE", "WINDTRE", "WINDTRE", "222", "88", "39"},
	},
	"ES": {
		{"Movistar", "Movistar", "Movistar", "214", "07", "34"},
		{"vodafone ES", "vodafone", "vodafone ES", "214", "01", "34"},
		{"Orange", "Orange", "Orange", "214", "03", "34"},
	},
	"NL": {
		{"KPN", "KPN", "KPN", "204", "08", "31"},
		{"vodafone NL", "vodafone", "vodafone NL", "204", "04", "31"},
		{"Odido", "Odido", "Odido", "204", "16", "31"},
	},
	"PL": {
		{"Plus", "Plus", "Plus", "260", "01", "48"},
		{"T-Mobile.pl", "T-Mobile", "T-Mobile.pl", "260", "02", "48"},
		{"Orange", "Orange", "Orange", "260", "03", "48"},
		{"Play", "Play", "Play", "260", "06", "48"},
	},
	"TR": {
		{"Turkcell", "Turkcell", "Turkcell", "286", "01", "90"},
		{"Vodafone TR", "Vodafone", "Vodafone TR", "286", "02", "90"},
		{"Turk Telekom", "TT", "Turk Telekom", "286", "03", "90"},
	},
	"BR": {
		{"VIVO", "VIVO", "VIVO", "724", "10", "55"},
		{"Claro BR", "Claro", "Claro BR", "724", "05", "55"},
		{"TIM BRASIL", "TIM", "TIM BRASIL", "724", "04", "55"},
	},
	"TH": {
		{"AIS", "AIS", "AIS", "520", "03", "66"},
		{"TRUE-H", "TRUE-H", "TRUE-H", "520", "04", "66"},
		{"dtac", "dtac", "dtac", "520", "05", "66"},
	},
	"VN": {
		{"Viettel Mobile", "Viettel", "Viettel", "452", "04", "84"},
		{"VINAPHONE", "VINAPHONE", "VINAPHONE", "452", "02", "84"},
		{"MOBIFONE", "MOBIFONE", "MOBIFONE", "452", "01", "84"},
	},
	"ID": {
		{"TELKOMSEL", "TSEL", "TELKOMSEL", "510", "10", "62"},
		{"INDOSAT", "IM3", "INDOSAT", "510", "01", "62"},
		{"XL Axiata", "XL", "XL", "510", "11", "62"},
	},
	"PH": {
		{"Globe Telecom", "Globe", "Globe", "515", "02", "63"},
		{"SMART", "SMART", "SMART", "515", "03", "63"},
		{"DITO", "DITO", "DITO", "515", "66", "63"},
	},
	"AE": {
		{"Etisalat", "etisalat", "Etisalat", "424", "02", "971"},
		{"du", "du", "du", "424", "03", "971"},
	},
	"SA": {
		{"STC", "STC", "STC", "420", "01", "966"},
		{"Mobily", "Mobily", "Mobily", "420", "03", "966"},
		{"Zain SA", "Zain", "Zain SA", "420", "04", "966"},
	},
	"CN": {
		{"CHINA MOBILE", "CMCC", "中国移动", "460", "00", "86"},
		{"CHN-UNICOM", "UNICOM", "中国联通", "460", "01", "86"},
		{"CHINA TELECOM", "CTCC", "中国电信", "460", "11", "86"},
	},
	"MO": {
		{"CTM", "CTM", "CTM", "455", "01", "853"},
		{"3 Macau", "3", "3", "455", "03", "853"},
	},
}

// Pick 按国家选一家运营商、生成这张 SIM 卡和基带的身份。seed 相同结果相同（一键新机换 seed 就换卡）。
// tac 是机型的 IMEI 前 8 位（型号核准码），空就用一个通用的。
func Pick(country, tac string, seed int64) Spec {
	r := rand.New(rand.NewSource(seed))
	list := carriers[strings.ToUpper(country)]
	absent := len(list) == 0
	if absent {
		// 表里没有这个国家（或者代理还没检测出国家）：当成没插 SIM 卡（真手机也常见），
		// 不要随便塞一张别国的卡——墨西哥的 IP 配香港的卡一眼就露馅。IMEI 照样有（没卡的手机也有 IMEI）。
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
	if absent {
		return Spec{IMEI: imei + luhn(imei), Absent: true, Signal: 16 + r.Intn(10), RSRP: 85 + r.Intn(20),
			Version: "MOLY.NR17.R1.TC8.PR1.SP.V1.P" + fmt.Sprint(10+r.Intn(40))}
	}
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
