package power

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// recorder 记录每次实际下发给系统的请求。
type recorder struct {
	mu    sync.Mutex
	calls []bool
	err   error
}

func (r *recorder) apply(keepAwake bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.calls = append(r.calls, keepAwake)
	return nil
}

func (r *recorder) snapshot() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.calls...)
}

func testManager(t *testing.T) (*Manager, *recorder) {
	t.Helper()
	r := &recorder{}
	m := newWith(r.apply)
	t.Cleanup(func() { m.Close() })
	return m, r
}

// waitCalls 等到记录条数达到 n；超时即失败。
func waitCalls(t *testing.T, r *recorder, n int) []bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := r.snapshot(); len(got) >= n {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待第 %d 次下发超时，当前 %v", n, r.snapshot())
	return nil
}

// ---------- 策略 ----------

func TestKeepAwakeOnlyWhileStreaming(t *testing.T) {
	cases := []struct {
		name    string
		running int
		enabled bool
		want    bool
	}{
		{"有任务在推且开了设置", 2, true, true},
		{"有任务在推但关了设置", 2, false, false},
		{"开了设置但没有任务在推", 0, true, false},
		{"都没有", 0, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, r := testManager(t)
			m.Update(c.running, c.enabled)
			if !c.want {
				// 期望不阻止休眠时，初始状态本就是不阻止，不该有任何下发
				time.Sleep(20 * time.Millisecond)
				if got := r.snapshot(); len(got) != 0 {
					t.Errorf("不需要阻止休眠时不该下发，实际 %v", got)
				}
				return
			}
			got := waitCalls(t, r, 1)
			if !got[0] {
				t.Errorf("应请求阻止休眠，实际 %v", got)
			}
		})
	}
}

func TestReleaseWhenLastTaskStops(t *testing.T) {
	m, r := testManager(t)
	m.Update(1, true)
	waitCalls(t, r, 1)

	m.Update(0, true)
	got := waitCalls(t, r, 2)
	if got[1] {
		t.Errorf("最后一个任务停止后应释放，实际 %v", got)
	}
}

func TestNoRedundantCalls(t *testing.T) {
	// 任务状态每秒都在变，若每次都往系统里捅一次，等于给自己加了个定时开销
	m, r := testManager(t)
	for i := 0; i < 10; i++ {
		m.Update(3, true)
	}
	waitCalls(t, r, 1)
	time.Sleep(30 * time.Millisecond)

	if got := r.snapshot(); len(got) != 1 {
		t.Errorf("状态未变化时应只下发一次，实际 %d 次: %v", len(got), got)
	}
}

func TestToggleBackAndForth(t *testing.T) {
	m, r := testManager(t)
	m.Update(1, true)
	waitCalls(t, r, 1)
	m.Update(0, true)
	waitCalls(t, r, 2)
	m.Update(1, true)
	got := waitCalls(t, r, 3)

	want := []bool{true, false, true}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("下发序列 = %v, 期望 %v", got, want)
		}
	}
}

// ---------- 生命周期 ----------

func TestCloseReleasesKeepAwake(t *testing.T) {
	// 进程退出时不释放，系统会一直被按着不许睡——用户合上盖子回来发现电池空了
	r := &recorder{}
	m := newWith(r.apply)
	m.Update(1, true)
	waitCalls(t, r, 1)

	m.Close()
	got := r.snapshot()
	if len(got) < 2 || got[len(got)-1] {
		t.Errorf("Close 应释放阻止休眠，下发序列 %v", got)
	}
}

func TestCloseWithoutKeepAwakeDoesNotPanic(t *testing.T) {
	m, _ := testManager(t)
	m.Close()
	m.Close() // 重复关闭要幂等：托盘退出与窗口关闭可能都会调
}

func TestUpdateAfterCloseIsIgnored(t *testing.T) {
	r := &recorder{}
	m := newWith(r.apply)
	m.Close()
	m.Update(5, true) // 不应 panic（向已关闭的 channel 发送）
	time.Sleep(20 * time.Millisecond)

	for _, c := range r.snapshot() {
		if c {
			t.Error("关闭之后不应再请求阻止休眠")
		}
	}
}

func TestApplyErrorIsSurfaced(t *testing.T) {
	// 系统调用失败必须让上层知道，否则用户以为设了不休眠、结果半夜断流
	r := &recorder{err: errors.New("拒绝访问")}
	var got error
	var mu sync.Mutex
	m := newWith(r.apply)
	m.OnError = func(err error) {
		mu.Lock()
		got = err
		mu.Unlock()
	}
	defer m.Close()

	m.Update(1, true)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		e := got
		mu.Unlock()
		if e != nil {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Error("系统调用失败时应回调 OnError")
}

// ---------- 真实系统调用 ----------

func TestRealSetExecutionState(t *testing.T) {
	if !supported() {
		t.Skip("当前平台不支持阻止休眠")
	}
	m := New()
	defer m.Close()

	m.Update(1, true)
	time.Sleep(50 * time.Millisecond)
	m.Update(0, true)
	time.Sleep(50 * time.Millisecond)
	// 走通不报错即可：系统实际的休眠计时器无法在单测里断言
}
