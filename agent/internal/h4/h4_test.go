package h4

import (
	"bytes"
	"testing"
)

func TestSplit(t *testing.T) {
	evt := []byte{0x04, 0x0e, 0x04, 0x01, 0x03, 0x0c, 0x00}                     // Command Complete（Reset）
	acl := append([]byte{0x02, 0x01, 0x20, 0x05, 0x01}, make([]byte, 0x105)...) // 长度 0x0105
	iso := append([]byte{0x05, 0x01, 0x00, 0x03, 0x40}, 1, 2, 3)                // 长度低 14 位 = 3
	stream := append(append(append(append([]byte{}, evt...), acl...), iso...), ResetCommand...)
	// 一个字节一个字节喂，拆出来的包要和原来一样
	var got [][]byte
	var rest []byte
	for _, b := range stream {
		rest = Split(append(rest, b), func(p []byte) { got = append(got, append([]byte{}, p...)) })
	}
	if len(rest) != 0 || len(got) != 4 {
		t.Fatalf("拆出 %d 个包，剩 %d 字节", len(got), len(rest))
	}
	for i, want := range [][]byte{evt, acl, iso, ResetCommand} {
		if !bytes.Equal(got[i], want) {
			t.Fatalf("第 %d 个包不对：%x", i, got[i][:min(8, len(got[i]))])
		}
	}
	if !IsReset(got[3]) || IsReset(got[0]) {
		t.Fatal("IsReset 判断错")
	}
	// 不认识的类型：整个交出去，不卡住
	n := 0
	if r := Split([]byte{0x99, 1, 2}, func(p []byte) { n += len(p) }); len(r) != 0 || n != 3 {
		t.Fatal("乱了的边界应该整个交出去")
	}
}
