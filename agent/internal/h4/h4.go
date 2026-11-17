// Package h4 蓝牙 HCI 的 H4 串口格式拆包（踩坑 #48）。
//
// 蓝牙 HAL 和芯片（rootcanal）之间是 H4：每个包第一个字节是类型，后面是头和数据，长度写在头里：
//
//	0x01 命令：操作码 2 + 长度 1          0x02 ACL 数据：句柄 2 + 长度 2（小端）
//	0x03 SCO 数据：句柄 2 + 长度 1        0x04 事件：事件码 1 + 长度 1
//	0x05 ISO 数据：句柄 2 + 长度 2（低 14 位）
//
// 小助手转发时按完整的包写进串口：蓝牙关了再开时清掉串口里没读走的旧数据，留下的一定是整包，
// HAL 不会读到半截包、对不上边界（踩坑 #48：重开蓝牙时初始化超时崩过一次）。
package h4

// ResetCommand HCI_Reset（HAL 每次初始化芯片第一条命令）。
var ResetCommand = []byte{0x01, 0x03, 0x0c, 0x00}

// packetLen buf 开头这个包一共多长；头还没收全返回 0；不认识的类型返回 -1。
func packetLen(buf []byte) int {
	if len(buf) == 0 {
		return 0
	}
	need := func(hdr int) bool { return len(buf) >= 1+hdr }
	switch buf[0] {
	case 0x01: // 命令
		if !need(3) {
			return 0
		}
		return 4 + int(buf[3])
	case 0x02: // ACL
		if !need(4) {
			return 0
		}
		return 5 + (int(buf[3]) | int(buf[4])<<8)
	case 0x03: // SCO
		if !need(3) {
			return 0
		}
		return 4 + int(buf[3])
	case 0x04: // 事件
		if !need(2) {
			return 0
		}
		return 3 + int(buf[2])
	case 0x05: // ISO
		if !need(4) {
			return 0
		}
		return 5 + (int(buf[3]) | int(buf[4]&0x3f)<<8)
	}
	return -1
}

// Split 从 buf 里拆出完整的包交给 emit，返回剩下没收全的部分。
// 遇到不认识的类型（边界已经乱了）：把剩下的整个交出去，从头再来，不卡住。
func Split(buf []byte, emit func(pkt []byte)) []byte {
	for len(buf) > 0 {
		n := packetLen(buf)
		if n < 0 {
			emit(buf)
			return nil
		}
		if n == 0 || len(buf) < n {
			return buf
		}
		emit(buf[:n])
		buf = buf[n:]
	}
	return buf
}

// IsReset 是不是 HCI_Reset 命令包。
func IsReset(pkt []byte) bool {
	return len(pkt) == 4 && pkt[0] == 0x01 && pkt[1] == 0x03 && pkt[2] == 0x0c && pkt[3] == 0x00
}
