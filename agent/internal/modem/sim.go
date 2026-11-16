package modem

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// SIM 卡上的文件（3GPP TS 51.011 / 31.102）。只做安卓开机会读的几个，别的回「文件不存在」（6A82）。
const (
	efICCID = 0x2FE2
	efSPN   = 0x6F46
	efAD    = 0x6FAD
	efLI    = 0x6F05
	efPL    = 0x2F05

	cmdReadBinary  = 176
	cmdReadRecord  = 178
	cmdGetResponse = 192
)

// simFile 一个文件：内容、是不是按记录存的（记录长度）。
type simFile struct {
	data   []byte
	record int
}

func (m *Modem) simFiles() map[int]simFile {
	s := m.spec
	files := map[int]simFile{
		efICCID: {data: swapBCD(s.ICCID, 10)},
		efAD:    {data: []byte{0x00, 0x00, 0x00, byte(len(s.MNC))}}, // 最后一字节：MNC 是 2 位还是 3 位
		efLI:    {data: []byte("en\xff\xff\xff\xff\xff\xff")},
		efPL:    {data: []byte("en\xff\xff\xff\xff\xff\xff")},
	}
	if s.SPN != "" {
		d := make([]byte, 17)
		for i := range d {
			d[i] = 0xff
		}
		d[0] = 0x01 // 显示条件：在注册网络上也显示 SPN
		copy(d[1:], s.SPN)
		files[efSPN] = simFile{data: d}
	}
	return files
}

// simIO 处理 AT+CRSM=<命令>,<文件ID>,<p1>,<p2>,<p3>[,<数据>[,<路径>]]，回 +CRSM: <sw1>,<sw2>[,<十六进制>]。
func (m *Modem) simIO(args string) []string {
	f := strings.Split(args, ",")
	if len(f) < 2 {
		return []string{"+CRSM: 106,128"}
	}
	cmd, _ := strconv.Atoi(strings.TrimSpace(f[0]))
	id, _ := strconv.Atoi(strings.TrimSpace(f[1]))
	file, ok := m.simFiles()[id]
	if !ok {
		return []string{"+CRSM: 106,130"} // 6A82 文件不存在
	}
	arg := func(i int) int {
		if i < len(f) {
			v, _ := strconv.Atoi(strings.TrimSpace(f[i]))
			return v
		}
		return 0
	}
	switch cmd {
	case cmdGetResponse:
		// 2G 格式的 GET RESPONSE（15 字节）：[2..3] 大小、[4..5] 文件 ID、[6]=04 EF、[13] 结构（00 透明 / 01 记录）、[14] 记录长度
		r := make([]byte, 15)
		r[2], r[3] = byte(len(file.data)>>8), byte(len(file.data))
		r[4], r[5] = byte(id>>8), byte(id)
		r[6] = 0x04
		r[12] = 0x02
		if file.record > 0 {
			r[13], r[14] = 0x01, byte(file.record)
		}
		return []string{fmt.Sprintf("+CRSM: 144,0,%s", strings.ToUpper(hex.EncodeToString(r)))}
	case cmdReadBinary:
		off := arg(2)<<8 | arg(3)
		n := arg(4)
		if n == 0 || off+n > len(file.data) {
			n = len(file.data) - off
		}
		if off < 0 || n < 0 || off > len(file.data) {
			return []string{"+CRSM: 107,0"}
		}
		return []string{fmt.Sprintf("+CRSM: 144,0,%s", strings.ToUpper(hex.EncodeToString(file.data[off:off+n])))}
	case cmdReadRecord:
		if file.record == 0 {
			return []string{"+CRSM: 105,129"}
		}
		rec := arg(2)
		start := (rec - 1) * file.record
		if rec < 1 || start+file.record > len(file.data) {
			return []string{"+CRSM: 106,131"}
		}
		return []string{fmt.Sprintf("+CRSM: 144,0,%s", strings.ToUpper(hex.EncodeToString(file.data[start:start+file.record])))}
	}
	return []string{"+CRSM: 109,0"} // 不支持的命令
}

// swapBCD SIM 上数字串的存法：两位一字节、低半字节在前，不够补 F。
func swapBCD(digits string, size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = 0xff
	}
	for i := 0; i < len(digits) && i/2 < size; i++ {
		d := digits[i] - '0'
		if i%2 == 0 {
			out[i/2] = 0xf0 | d
		} else {
			out[i/2] = out[i/2]&0x0f | d<<4
		}
	}
	return out
}
