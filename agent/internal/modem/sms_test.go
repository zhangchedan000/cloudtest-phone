package modem

import (
	"strings"
	"testing"
	"time"
)

func TestDeliverPDU(t *testing.T) {
	// 发件号 10086、正文「你好」。
	pdu := deliverPDU("10086", "你好", time.Date(2026, 11, 27, 9, 30, 0, 0, time.UTC))
	// SMSC=00, 首字节=04, 地址=05 81 0180F6（半字节交换，奇数补F）, PID=00, DCS=08
	if !strings.HasPrefix(pdu, "000405810180F60008") {
		t.Fatalf("PDU 头不对：%s", pdu)
	}
	// 正文 UCS2：你=4F60 好=597D，UDL=04
	if !strings.HasSuffix(pdu, "044F60597D") {
		t.Fatalf("PDU 正文不对：%s", pdu)
	}
	// 时间戳 7 字节：62 11 72 90 03 00 + TZ（UTC=00）
	if !strings.Contains(pdu, "62117290030000") {
		t.Fatalf("时间戳不对：%s", pdu)
	}
}

func TestAddrIntl(t *testing.T) {
	// +8613800138000：国际号 91，奇数位补 F
	got := addr("+8613800138000")
	if got != "0D91683108108300F0" {
		t.Fatalf("国际发件号地址不对：%s", got)
	}
}

func TestInjectAbsentFails(t *testing.T) {
	m := New(Spec{Absent: true}, &strings.Builder{})
	if err := m.InjectSMS("10086", "hi"); err == nil {
		t.Fatal("没插卡应该报错")
	}
}
