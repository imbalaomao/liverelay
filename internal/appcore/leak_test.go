package appcore

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/imbalaomao/liverelay/internal/config"
)

// settle 等 goroutine 数稳定下来，避免测到调度器的瞬时抖动。
func settle(target int, d time.Duration) int {
	deadline := time.Now().Add(d)
	n := runtime.NumGoroutine()
	for time.Now().Before(deadline) {
		n = runtime.NumGoroutine()
		if n <= target {
			return n
		}
		time.Sleep(20 * time.Millisecond)
	}
	return n
}

func TestCoreDoesNotLeakGoroutines(t *testing.T) {
	// Core 起了探测循环、微博复检循环、还有若干在途探测。
	// 退出时收不干净的话，反复开关设置页就会把 goroutine 越堆越多
	runtime.GC()
	before := settle(0, 500*time.Millisecond)

	for i := 0; i < 5; i++ {
		c, err := New(t.TempDir(), config.Default())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
			Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}}); err != nil {
			t.Fatal(err)
		}
		c.Close()
	}

	runtime.GC()
	after := settle(before+2, 3*time.Second)
	if after > before+2 {
		t.Errorf("goroutine 数 %d → %d，疑似泄漏\n%s", before, after, dumpGoroutines())
	}
}

func TestCloseWaitsForBackgroundLoops(t *testing.T) {
	// Close 返回后后台循环必须已经停了，否则退出流程会留下野进程
	c, err := New(t.TempDir(), config.Default())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close 超过 5 秒未返回")
	}
}

func dumpGoroutines() string {
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)
	s := string(buf[:n])
	// 只留前几段，够定位就行
	if parts := strings.SplitN(s, "\n\n", 6); len(parts) > 5 {
		return strings.Join(parts[:5], "\n\n")
	}
	return s
}
