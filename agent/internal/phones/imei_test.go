package phones

import (
	"bytes"
	"testing"
)

// 构造一个像 .so 的 blob：别处有数字但不是孤立15位，只有一处 \x00<15位>\x00。
func TestImeiPatchLogic(t *testing.T) {
	imei := "867400022047199"
	// 干扰项：14位、16位、没被null包住的15位
	blob := []byte("\x00abc\x0012345678901234\x00" + // 14位
		"xx99999999999999999xx" + // 没null包住
		"\x00" + imei + "\x00" + // 目标：孤立15位
		"E_REQUEST_NOT_SUPPORTED\x00")
	ms := imei15.FindAllSubmatchIndex(blob, -1)
	if len(ms) != 1 {
		t.Fatalf("期望唯一命中，实际 %d 个", len(ms))
	}
	off := ms[0][2]
	if string(blob[off:off+15]) != imei {
		t.Fatalf("定位错：%q", blob[off:off+15])
	}
	// 打补丁：替换成新 IMEI
	newImei := "350298075645453"
	patched := append([]byte(nil), blob...)
	copy(patched[off:off+15], []byte(newImei))
	if !bytes.Contains(patched, []byte("\x00"+newImei+"\x00")) {
		t.Fatalf("补丁后找不到新 IMEI")
	}
	if bytes.Contains(patched, []byte(imei)) {
		t.Fatalf("补丁后旧 IMEI 还在")
	}
	// 其余字节不变（长度一致、只动了那15字节）
	if len(patched) != len(blob) {
		t.Fatalf("长度变了 %d != %d", len(patched), len(blob))
	}
	for i := range blob {
		if i >= off && i < off+15 {
			continue
		}
		if patched[i] != blob[i] {
			t.Fatalf("第 %d 字节被误改", i)
		}
	}
}

// 多处命中 / 零命中都不该被当成唯一（prepareImei 会 off<0 跳过）。
func TestImeiPatchAmbiguous(t *testing.T) {
	two := []byte("\x00867400022047199\x00pad\x00350298075645453\x00")
	if n := len(imei15.FindAllSubmatchIndex(two, -1)); n != 2 {
		t.Fatalf("两处应命中2，实际 %d", n)
	}
	none := []byte("no fifteen digit string here 123\x00")
	if n := len(imei15.FindAllSubmatchIndex(none, -1)); n != 0 {
		t.Fatalf("零处应命中0，实际 %d", n)
	}
}
