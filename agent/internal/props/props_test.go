package props

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// builder 按 bionic 的方式在内存里建一个属性区（只为测试）。
type builder struct{ data []byte }

func newBuilder() *builder {
	b := &builder{data: make([]byte, btHeaderSize+valueMax)} // 根节点 + 脏备份区
	return b
}

func (b *builder) u32(off int) uint32    { return binary.LittleEndian.Uint32(b.data[off:]) }
func (b *builder) put(off int, v uint32) { binary.LittleEndian.PutUint32(b.data[off:], v) }
func align4(n int) int                   { return (n + 3) &^ 3 }
func (b *builder) alloc(n int) int {
	off := len(b.data)
	b.data = append(b.data, make([]byte, align4(n))...)
	return off
}
func (b *builder) name(n int) []byte {
	l := int(b.u32(n))
	return b.data[n+btHeaderSize : n+btHeaderSize+l]
}

func (b *builder) newBT(name string) int {
	off := b.alloc(btHeaderSize + len(name) + 1)
	b.put(off, uint32(len(name)))
	copy(b.data[off+btHeaderSize:], name)
	return off
}

func (b *builder) add(name, value string) {
	cur := 0
	for _, part := range strings.Split(name, ".") {
		if b.u32(cur+16) == 0 {
			n := b.newBT(part)
			b.put(cur+16, uint32(n))
			cur = n
			continue
		}
		n := int(b.u32(cur + 16))
		for {
			c := cmp([]byte(part), b.name(n))
			if c == 0 {
				break
			}
			field := n + 8
			if c > 0 {
				field = n + 12
			}
			if b.u32(field) == 0 {
				nn := b.newBT(part)
				b.put(field, uint32(nn))
				n = nn
				break
			}
			n = int(b.u32(field))
		}
		cur = n
	}
	pi := b.alloc(4 + valueMax + len(name) + 1)
	b.put(pi, uint32(len(value))<<24)
	copy(b.data[pi+4:], value)
	copy(b.data[pi+4+valueMax:], name)
	b.put(cur+4, uint32(pi))
}

func (b *builder) file(t *testing.T, dir, name string) {
	hdr := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(b.data)))
	binary.LittleEndian.PutUint32(hdr[8:], propMagic)
	if err := os.WriteFile(filepath.Join(dir, name), append(hdr, b.data...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHideAndSet(t *testing.T) {
	Dir = t.TempDir()
	b := newBuilder()
	for _, kv := range [][2]string{
		{"ro.boot.redroid_width", "1220"}, {"ro.boot.redroid_height", "2712"}, {"ro.boot.hardware", "redroid"},
		{"ro.boot.serialno", "ABC"}, {"ro.hardware", "redroid"}, {"persist.sys.timezone", "Asia/Hong_Kong"},
	} {
		b.add(kv[0], kv[1])
	}
	b.file(t, Dir, "u:object_r:default_prop:s0")
	_ = os.WriteFile(filepath.Join(Dir, "property_info"), []byte("x"), 0o644)

	if v, ok := Get("ro.boot.redroid_width"); !ok || v != "1220" {
		t.Fatalf("读：%q %v", v, ok)
	}
	if ok, err := Hide("ro.boot.redroid_width"); !ok || err != nil {
		t.Fatalf("藏：%v %v", ok, err)
	}
	if _, ok := Get("ro.boot.redroid_width"); ok {
		t.Fatal("藏了还能读到")
	}
	if v, ok := Get("ro.boot.redroid_height"); !ok || v != "2712" {
		t.Fatal("别的属性不能受影响")
	}
	if ok, _ := Hide("ro.boot.nothing"); ok {
		t.Fatal("没有的属性不该算藏了")
	}
	if ok, err := Set("ro.hardware", "mt6895"); !ok || err != nil {
		t.Fatalf("改：%v %v", ok, err)
	}
	if v, _ := Get("ro.hardware"); v != "mt6895" {
		t.Fatalf("改完是 %q", v)
	}
	if v, _ := Get("ro.boot.serialno"); v != "ABC" {
		t.Fatal("别的属性不能受影响")
	}
}
