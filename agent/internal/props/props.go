// Package props 直接改安卓的系统属性表（和 Magisk 的 resetprop 一个原理，踩坑 #63）。
//
// 安卓的属性存在 /dev/__properties__/ 下的共享内存文件里（每个 SELinux 上下文一个文件），所有进程只读映射。
// ro. 开头的属性 setprop 改不了，但 root 可以直接改这些文件：
//   - Hide：把属性从查找树上摘掉（树节点的 prop 偏移清零）——getprop、SystemProperties.get、列全部属性都看不到了；
//   - Set：原地改值（同时更新 serial，读的进程会拿到新值）。
//
// 用途：Redroid 开机要用的 ro.boot.redroid_* 等属性，开机用完以后藏起来（App 能读系统属性，带 redroid 字样一看就是容器）。
//
// 内存布局（bionic system_properties/prop_area.h、prop_info.h，32/64 位一样）：
//
//	prop_area：bytes_used u32, serial u32, magic u32 (0x504f5250 "PROP"), version u32, reserved[28] u32，然后 data
//	prop_bt（树节点，偏移相对 data）：namelen u32, prop u32, left u32, right u32, children u32, name[namelen+1]
//	prop_info：serial u32, value[92], name[]；serial 高 8 位是值的长度，最低位是「正在写」标记
//	查找：按 . 分段，每段在 children 指向的二叉树里找（先比长度，再比内容）。
package props

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"
)

const (
	headerSize   = 128
	propMagic    = 0x504f5250
	valueMax     = 92
	btHeaderSize = 20
	longFlag     = 1 << 16 // prop_info 里 serial 的 kLongFlag：值太长另外存，不能原地改
)

// Dir 属性文件目录（测试时换掉）。
var Dir = "/dev/__properties__"

type area struct {
	b []byte
}

func (a area) u32(off int) uint32 { return binary.LittleEndian.Uint32(a.b[off:]) }

// atomicU32 共享内存里的 4 字节，用原子写（别的进程同时在读）。
func (a area) atomicU32(off int) *uint32 { return (*uint32)(unsafe.Pointer(&a.b[off])) }

// node 数据区偏移 → 树节点在整个文件里的偏移。
func node(dataOff uint32) int { return headerSize + int(dataOff) }

func (a area) nodeName(n int) []byte {
	l := int(a.u32(n))
	return a.b[n+btHeaderSize : n+btHeaderSize+l]
}

// cmp 和 bionic cmp_prop_name 一样：先比长度，再比内容。
func cmp(name []byte, other []byte) int {
	if len(name) < len(other) {
		return -1
	}
	if len(name) > len(other) {
		return 1
	}
	return bytes.Compare(name, other)
}

// find 找属性对应的树节点（文件偏移），没有返回 -1。
func (a area) find(name string) int {
	cur := node(0)
	for _, part := range strings.Split(name, ".") {
		children := a.u32(cur + 16)
		if children == 0 {
			return -1
		}
		n := node(children)
		for {
			c := cmp([]byte(part), a.nodeName(n))
			if c == 0 {
				break
			}
			next := a.u32(n + 8) // left
			if c > 0 {
				next = a.u32(n + 12) // right
			}
			if next == 0 {
				return -1
			}
			n = node(next)
		}
		cur = n
	}
	return cur
}

// eachArea 打开每个属性文件（可写映射），交给 fn；fn 返回 true 表示处理完了。
func eachArea(fn func(a area) (bool, error)) error {
	ents, err := os.ReadDir(Dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.IsDir() || e.Name() == "property_info" || e.Name() == "properties_serial" {
			continue
		}
		done, err := withArea(filepath.Join(Dir, e.Name()), fn)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	return nil
}

func withArea(path string, fn func(a area) (bool, error)) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, nil // 打不开的跳过（不是属性文件）
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() < headerSize {
		return false, nil
	}
	b, err := syscall.Mmap(int(f.Fd()), 0, int(st.Size()), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return false, nil
	}
	defer syscall.Munmap(b)
	a := area{b: b}
	if a.u32(8) != propMagic {
		return false, nil
	}
	return fn(a)
}

// Hide 把属性藏起来（从查找树上摘掉）。没有这个属性返回 false。
func Hide(name string) (bool, error) {
	found := false
	err := eachArea(func(a area) (bool, error) {
		n := a.find(name)
		if n < 0 || a.u32(n+4) == 0 {
			return false, nil
		}
		atomic.StoreUint32(a.atomicU32(n+4), 0)
		found = true
		return true, nil
	})
	return found, err
}

// Get 读属性值（自检用）。
func Get(name string) (string, bool) {
	var val string
	var ok bool
	_ = eachArea(func(a area) (bool, error) {
		n := a.find(name)
		if n < 0 || a.u32(n+4) == 0 {
			return false, nil
		}
		pi := node(a.u32(n + 4))
		v := a.b[pi+4 : pi+4+valueMax]
		if i := bytes.IndexByte(v, 0); i >= 0 {
			v = v[:i]
		}
		val, ok = string(v), true
		return true, nil
	})
	return val, ok
}

// Set 原地改值（只能改已有的、值不超过 91 字节的属性）。
func Set(name, value string) (bool, error) {
	if len(value) >= valueMax {
		return false, fmt.Errorf("值太长（最多 %d 字节）", valueMax-1)
	}
	found := false
	err := eachArea(func(a area) (bool, error) {
		n := a.find(name)
		if n < 0 || a.u32(n+4) == 0 {
			return false, nil
		}
		pi := node(a.u32(n + 4))
		serial := a.atomicU32(pi)
		old := atomic.LoadUint32(serial)
		if old&longFlag != 0 {
			return false, fmt.Errorf("%s 是长属性，不能原地改", name)
		}
		// 和 bionic SystemProperties::Update 一样：旧值先抄到「脏备份区」（数据区第 20 字节起，紧跟根节点），
		// 标「正在写」（这期间读的进程去读备份区），写新值，最后写新长度和序号
		v := a.b[pi+4 : pi+4+valueMax]
		backup := a.b[headerSize+btHeaderSize : headerSize+btHeaderSize+valueMax]
		copy(backup, v)
		dirty := old | 1
		atomic.StoreUint32(serial, dirty)
		copy(v, value)
		for i := len(value); i < valueMax; i++ {
			v[i] = 0
		}
		atomic.StoreUint32(serial, (uint32(len(value))<<24)|((dirty+1)&0xffffff))
		found = true
		return true, nil
	})
	return found, err
}
