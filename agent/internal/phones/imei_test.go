package phones

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// str 模式（安卓14）：唯一 null 包住的 15 位串 → 定位 + 替换。
func TestImeiStrMode(t *testing.T) {
	imei := defImei
	blob := []byte("\x00abc\x0012345678901234\x00" + // 14位干扰
		"xx99999999999999999xx" + // 没null包住
		"\x00" + imei + "\x00" +
		"E_REQUEST_NOT_SUPPORTED\x00")
	ms := imei15re.FindAllSubmatchIndex(blob, -1)
	if len(ms) != 1 {
		t.Fatalf("期望唯一命中，实际 %d", len(ms))
	}
	plan := rilPlan{Kind: "str", StrOff: int64(ms[0][2])}
	data := append([]byte(nil), blob...)
	if err := applyImeiPatch(data, plan, "350298075645453"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("\x00350298075645453\x00")) {
		t.Fatal("补丁后找不到新 IMEI")
	}
	if bytes.Contains(data, []byte(imei)) {
		t.Fatal("旧 IMEI 还在")
	}
	if len(data) != len(blob) {
		t.Fatal("长度变了")
	}
}

// a15 默认 IMEI 那 8 条 movz/movk 的真实机器码（从 libril-modem-lib.so v15 反汇编抄的）。
var a15DefaultInsns = []uint32{
	0x5286c708, // movz w8, #0x3638
	0x72a686e8, // movk w8, #0x3437, lsl 16
	0x52860688, // movz w8, #0x3034
	0x72a60608, // movk w8, #0x3030, lsl 16
	0xd2864648, // movz x8, #0x3232
	0xf2a68608, // movk x8, #0x3430, lsl 16
	0xf2c626e8, // movk x8, #0x3137, lsl 32
	0xf2e72728, // movk x8, #0x3939, lsl 48
}

// imm 模式（安卓15）：构造带这 8 条指令的 .text，定位→补丁→重解码验证拼出新 IMEI。
func TestImeiImmMode(t *testing.T) {
	// 验证这 8 条机器码确实解出 a15Pattern 的立即数（即它们拼的就是 defImei）
	for i, ins := range a15DefaultInsns {
		imm, ok := movImm16(ins)
		if !ok || imm != a15Pattern[i].imm {
			t.Fatalf("第%d条机器码 %08x 解出 imm=%#x ok=%v，期望 %#x", i, ins, imm, ok, a15Pattern[i].imm)
		}
	}
	// 组装 .text：前面塞点 nop / 不相关指令，再把 8 条按序放进去，中间插 nop
	nop := uint32(0xd503201f)
	var words []uint32
	words = append(words, nop, nop, 0x910203fd) // 开头无关指令
	for _, ins := range a15DefaultInsns {
		words = append(words, ins, nop) // 每条后插一个 nop 测跳过
	}
	words = append(words, nop, 0xd65f03c0) // ret
	data := make([]byte, len(words)*4)
	for i, w := range words {
		binary.LittleEndian.PutUint32(data[i*4:], w)
	}
	// 定位
	slots := findImmPlan(data, 0, int64(len(data)))
	if slots == nil || len(slots) != 8 {
		t.Fatalf("findImmPlan 没找全，slots=%v", slots)
	}
	// 补丁成新 IMEI
	newImei := "354881770022452"
	plan := rilPlan{Kind: "imm", Imm: slots}
	if err := applyImeiPatch(data, plan, newImei); err != nil {
		t.Fatal(err)
	}
	// 重解码：按 lo/hi 映射把每条立即数的两位填回，重建 IMEI 应 == newImei
	rebuilt := []byte("000000000000000")
	for i, sl := range slots {
		w := binary.LittleEndian.Uint32(data[sl.Off:])
		imm, ok := movImm16(w)
		if !ok {
			t.Fatalf("补丁后第%d条不是 mov 了：%08x", i, w)
		}
		lo := byte(imm & 0xFF)   // 低字节 = lo 位数字
		hi := byte(imm >> 8)     // 高字节 = hi 位数字
		rebuilt[sl.Lo] = lo
		rebuilt[sl.Hi] = hi
	}
	if string(rebuilt) != newImei {
		t.Fatalf("重建 IMEI=%q，期望 %q", rebuilt, newImei)
	}
	// 原始默认指令拼的应是 defImei（同法验证映射本身对）
	rebuilt2 := []byte("000000000000000")
	for i, p := range a15Pattern {
		imm := p.imm
		rebuilt2[p.lo] = byte(imm & 0xFF)
		rebuilt2[p.hi] = byte(imm >> 8)
		_ = i
	}
	if string(rebuilt2) != defImei {
		t.Fatalf("a15Pattern 映射拼出 %q，期望 %q", rebuilt2, defImei)
	}
}
