package phones

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.b.Bytes()...)
}

// 每次停顿后补一个 AUD；连着写的数据中间不插。
func TestCopyWithFrameEnds(t *testing.T) {
	r, w := io.Pipe()
	var out lockedBuf
	done := make(chan error, 1)
	go func() { done <- copyWithFrameEnds(&out, r, 10*time.Millisecond) }()

	f1 := []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 0, 1, 0x65, 9, 9}
	f2a, f2b := []byte{0, 0, 0, 1, 0x41, 7}, []byte{7, 7}
	_, _ = w.Write(f1)
	time.Sleep(60 * time.Millisecond) // 停顿：第一帧写完
	_, _ = w.Write(f2a)
	_, _ = w.Write(f2b) // 紧接着写：同一帧，中间不能插 AUD
	time.Sleep(60 * time.Millisecond)
	_ = w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var want []byte
	want = append(want, f1...)
	want = append(want, audNAL...)
	want = append(want, f2a...)
	want = append(want, f2b...)
	want = append(want, audNAL...)
	if got := out.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("输出不对：\n got % x\nwant % x", got, want)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("浏览器断开") }

// 浏览器断开：返回错误，并且不卡住写端（screenrecord 还能继续写完退出）。
func TestCopyWithFrameEndsWriterGone(t *testing.T) {
	r, w := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- copyWithFrameEnds(failWriter{}, r, 10*time.Millisecond) }()
	_, _ = w.Write([]byte{0, 0, 0, 1, 0x65})
	if err := <-done; err == nil {
		t.Fatal("应该返回错误")
	}
	wrote := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			_, _ = w.Write(make([]byte, 1024))
		}
		close(wrote)
	}()
	select {
	case <-wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("写端被卡住了")
	}
	_ = w.Close()
}
