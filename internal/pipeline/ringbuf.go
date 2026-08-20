package pipeline

import (
	"io"
	"sync"
)

// Ring 是有界字节环形缓冲：Write 永不阻塞、缓冲永不扩容；
// 满时丢弃最旧数据并累计 Dropped（落后指标）。内存溢出在机制上不可能发生。
type Ring struct {
	mu      sync.Mutex
	cond    *sync.Cond
	buf     []byte
	r, w    int
	used    int
	closed  bool
	Dropped int64
}

func NewRing(capacity int) *Ring {
	g := &Ring{buf: make([]byte, capacity)}
	g.cond = sync.NewCond(&g.mu)
	return g
}

func (g *Ring) Write(p []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	if len(p) > len(g.buf) { // 单次写入超容量：仅保留尾部
		drop := len(p) - len(g.buf)
		p = p[drop:]
		g.Dropped += int64(drop)
	}
	for len(p) > len(g.buf)-g.used { // 空间不足：逐字节丢弃最旧
		g.r = (g.r + 1) % len(g.buf)
		g.used--
		g.Dropped++
	}
	for _, b := range p {
		g.buf[g.w] = b
		g.w = (g.w + 1) % len(g.buf)
		g.used++
	}
	g.cond.Broadcast()
}

func (g *Ring) Read(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.used == 0 && !g.closed {
		g.cond.Wait()
	}
	if g.used == 0 && g.closed {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && g.used > 0 {
		p[n] = g.buf[g.r]
		g.r = (g.r + 1) % len(g.buf)
		g.used--
		n++
	}
	return n, nil
}

func (g *Ring) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.cond.Broadcast()
}
