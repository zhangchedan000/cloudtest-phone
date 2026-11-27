package modem

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
)

// 收短信（2026-11-27 用户要求，对齐魔云腾）：管理端填一条短信，手机就「收到」一条（带通知，进短信 App）。
//
// 做法：我们就是基带。往电话驱动（reference-ril / cuttlefish-ril）主动上报一条 `+CMT: ,<长度><CR><LF><PDU>`，
// 它当成「新短信直接下发」交给框架，框架写进短信库、弹通知。PDU 是 GSM 03.40 的 SMS-DELIVER（收到的短信）格式，
// 正文统一用 UCS2（两字节一个字，中英文都行）。一条最多 70 个字，超了就拆成几条分别发（简单稳妥）。
//
// 注：content insert 进 content://sms 不行——安卓只让「默认短信 App」写短信库，别人写了静默丢弃（实测 EXIT=0 但查不到）。

const smsMaxUCS2Chars = 70 // 单条 UCS2 上限（140 字节 / 2）

// InjectSMS 让手机收到一条来自 sender 的短信（正文 text）。sender 走号码（纯数字，可带 +）。
func (m *Modem) InjectSMS(sender, text string) error {
	if m.spec.Absent {
		return fmt.Errorf("没插 SIM 卡，收不了短信")
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return fmt.Errorf("短信内容是空的")
	}
	now := time.Now()
	for i := 0; i < len(runes); i += smsMaxUCS2Chars {
		end := i + smsMaxUCS2Chars
		if end > len(runes) {
			end = len(runes)
		}
		pdu := deliverPDU(sender, string(runes[i:end]), now)
		m.pushCMT(pdu)
	}
	return nil
}

// pushCMT 主动上报一条新短信（+CMT 和 PDU 必须是紧挨着的两行，不能像普通回复那样中间夹空行）。
func (m *Modem) pushCMT(pduHex string) {
	// 长度 = 去掉 SMSC 那一段后的 TPDU 字节数。SMSC 这里是 "00"（1 字节，用默认短信中心）。
	tpduLen := (len(pduHex) - 2) / 2
	m.wmu.Lock()
	defer m.wmu.Unlock()
	_, _ = fmt.Fprintf(m.w, "\r\n+CMT: ,%d\r\n%s\r\n", tpduLen, pduHex)
}

// deliverPDU 拼一条 SMS-DELIVER 的 PDU（十六进制大写）：SMSC + 首字节 + 发件号 + PID + DCS(UCS2) + 时间 + 正文。
func deliverPDU(sender, text string, ts time.Time) string {
	var b strings.Builder
	b.WriteString("00")   // SMSC：长度 0，用默认短信中心
	b.WriteString("04")   // 首字节：SMS-DELIVER（TP-MTI=00）、TP-MMS=1（没有更多）
	b.WriteString(addr(sender)) // 发件号地址
	b.WriteString("00")   // TP-PID
	b.WriteString("08")   // TP-DCS：UCS2（两字节一个字）
	b.WriteString(scts(ts)) // TP-SCTS 时间戳（7 字节 BCD）
	ud := ucs2(text)
	b.WriteString(fmt.Sprintf("%02X", len(ud)/2)) // TP-UDL：UCS2 下是字节数
	b.WriteString(ud)
	return b.String()
}

// addr 发件号地址段：地址长度(数字个数) + 类型(国际 91 / 国内 81) + BCD 半字节交换(奇数末尾补 F)。
func addr(sender string) string {
	ton := "81"
	digits := sender
	if strings.HasPrefix(digits, "+") {
		ton = "91"
		digits = digits[1:]
	}
	var only strings.Builder
	for _, r := range digits {
		if r >= '0' && r <= '9' {
			only.WriteRune(r)
		}
	}
	d := only.String()
	if d == "" {
		d = "0"
	}
	var swapped strings.Builder
	for i := 0; i < len(d); i += 2 {
		if i+1 < len(d) {
			swapped.WriteByte(d[i+1])
			swapped.WriteByte(d[i])
		} else {
			swapped.WriteByte('F')
			swapped.WriteByte(d[i])
		}
	}
	return fmt.Sprintf("%02X", len(d)) + ton + swapped.String()
}

// scts 时间戳：YY MM DD HH MM SS TZ，每个字节十进制两位、半字节交换（27.005）。
// TZ 是时区偏移（15 分钟为单位）的 BCD，负时区在交换后的低半字节置符号位（+8）。
func scts(t time.Time) string {
	y, mo, da := t.Date()
	h, mi, se := t.Clock()
	_, off := t.Zone()
	q := off / 900
	sign := 0
	if q < 0 {
		sign = 0x8
		q = -q
	}
	tz := fmt.Sprintf("%X%X", q%10, (q/10)%10|sign)
	return bcd2(y%100) + bcd2(int(mo)) + bcd2(da) + bcd2(h) + bcd2(mi) + bcd2(se) + tz
}

// swapBCD 把一个 0~99 的数变成「十进制两位、半字节交换」的两位十六进制字符（如 27 → "72"）。
func bcd2(n int) string {
	n %= 100
	hi := n / 10
	lo := n % 10
	return fmt.Sprintf("%d%d", lo, hi)
}

// ucs2 正文转 UTF-16BE 的十六进制。
func ucs2(s string) string {
	var b strings.Builder
	for _, u := range utf16.Encode([]rune(s)) {
		b.WriteString(fmt.Sprintf("%04X", u))
	}
	return b.String()
}
