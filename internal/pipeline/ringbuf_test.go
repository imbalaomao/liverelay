package pipeline

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestRingNeverExceedsCapacityAndKeepsNewest(t *testing.T) {
	r := NewRing(1024)
	data := make([]byte, 10*1024)
	for i := range data {
		data[i] = byte(i % 251)
	}
	r.Write(data)
	if r.Dropped == 0 {
		t.Fatal("应发生丢弃")
	}
	out := make([]byte, 2048)
	n, err := r.Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if n > 1024 {
		t.Fatalf("容量红线被突破: 读出 %d 字节", n)
	}
	if !bytes.Equal(out[:n], data[len(data)-n:]) {
		t.Fatal("背压应丢弃最旧数据、保留最新数据")
	}
}

func TestRingBlocksUntilData(t *testing.T) {
	r := NewRing(64)
	done := make(chan int, 1)
	go func() {
		buf := make([]byte, 4)
		n, _ := r.Read(buf)
		done <- n
	}()
	select {
	case <-done:
		t.Fatal("无数据时 Read 不应返回")
	case <-time.After(50 * time.Millisecond):
	}
	r.Write([]byte("abcd"))
	select {
	case n := <-done:
		if n != 4 {
			t.Fatalf("n=%d, 期望 4", n)
		}
	case <-time.After(time.Second):
		t.Fatal("写入后 Read 应解除阻塞")
	}
}

func TestRingCloseEOF(t *testing.T) {
	r := NewRing(64)
	r.Write([]byte("ab"))
	r.Close()
	buf := make([]byte, 8)
	if n, err := r.Read(buf); n != 2 || err != nil {
		t.Fatalf("排空前 Read: n=%d err=%v", n, err)
	}
	if _, err := r.Read(buf); err != io.EOF {
		t.Fatalf("排空后应返回 io.EOF, 实际 %v", err)
	}
}
