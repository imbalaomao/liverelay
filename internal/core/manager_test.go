package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imbalaomao/liverelay/internal/config"
)

type fakeRunner struct {
	exit    chan ExitInfo
	started chan struct{}
	stop    chan struct{}
	once    sync.Once
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		exit:    make(chan ExitInfo, 1),
		started: make(chan struct{}),
		stop:    make(chan struct{}),
	}
}

func (f *fakeRunner) Start(ctx context.Context) error { close(f.started); return nil }
func (f *fakeRunner) Wait() ExitInfo {
	select {
	case e := <-f.exit:
		return e
	case <-f.stop:
		return ExitInfo{Err: errors.New("killed")}
	}
}
func (f *fakeRunner) Stop() error { f.once.Do(func() { close(f.stop) }); return nil }

type fakeFactory struct {
	mu    sync.Mutex
	fakes []*fakeRunner
}

func (ff *fakeFactory) make(t config.Task) Runner {
	ff.mu.Lock()
	defer ff.mu.Unlock()
	f := newFakeRunner()
	ff.fakes = append(ff.fakes, f)
	return f
}

func (ff *fakeFactory) waitN(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ff.mu.Lock()
		if len(ff.fakes) >= n {
			ff.mu.Unlock()
			return
		}
		ff.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待第 %d 个 runner 超时", n)
}

func (ff *fakeFactory) get(i int) *fakeRunner {
	ff.mu.Lock()
	defer ff.mu.Unlock()
	return ff.fakes[i]
}

func testCfg(max int, ids ...string) *config.Config {
	c := config.Default()
	c.Settings.MaxConcurrent = max
	for _, id := range ids {
		c.Tasks = append(c.Tasks, config.Task{
			ID: id, Name: id, SourceURL: "sim://x", Quality: "best",
			Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://127.0.0.1/x"}},
		})
	}
	return c
}

func waitState(t *testing.T, m *Manager, id string, want State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.State(id) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待状态 %s 超时，当前 %s", want, m.State(id))
}

func TestManagerNormalLifecycle(t *testing.T) {
	ff := &fakeFactory{}
	m := NewManager(testCfg(2, "a"), ff.make, nil)
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	ff.waitN(t, 1)
	<-ff.get(0).started
	waitState(t, m, "a", StateRunning)
	ff.get(0).exit <- ExitInfo{Normal: true}
	waitState(t, m, "a", StateIdle)
}

// eventLog 无损记录状态事件。瞬态状态（如 reconnecting 在毫秒级退避下）
// 无法用轮询 State() 观察到，必须通过事件流断言。
type eventLog struct {
	mu     sync.Mutex
	states []State
	msgs   []string
}

func (e *eventLog) record(ev Event) {
	e.mu.Lock()
	e.states = append(e.states, ev.State)
	e.msgs = append(e.msgs, ev.Msg)
	e.mu.Unlock()
}

func (e *eventLog) waitSeen(t *testing.T, want State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		for _, s := range e.states {
			if s == want {
				e.mu.Unlock()
				return
			}
		}
		e.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("事件流中未出现状态 %s，已记录: %v", want, e.states)
}

func TestManagerReconnectThenNormal(t *testing.T) {
	ff := &fakeFactory{}
	log := &eventLog{}
	m := NewManager(testCfg(2, "a"), ff.make, log.record)
	m.newBackoff = func() *Backoff { return &Backoff{Min: time.Millisecond, Max: 4 * time.Millisecond} }
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	ff.waitN(t, 1)
	<-ff.get(0).started
	ff.get(0).exit <- ExitInfo{Err: errors.New("断流")}
	log.waitSeen(t, StateReconnecting)
	ff.waitN(t, 2)
	<-ff.get(1).started
	ff.get(1).exit <- ExitInfo{Normal: true}
	waitState(t, m, "a", StateIdle)
}

func TestManagerQueue(t *testing.T) {
	ff := &fakeFactory{}
	m := NewManager(testCfg(1, "a", "b"), ff.make, nil)
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	ff.waitN(t, 1)
	<-ff.get(0).started
	if err := m.Start("b"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "b", StateQueued)
	ff.get(0).exit <- ExitInfo{Normal: true}
	ff.waitN(t, 2)
	waitState(t, m, "b", StateRunning)
}

func TestManagerStop(t *testing.T) {
	ff := &fakeFactory{}
	m := NewManager(testCfg(2, "a"), ff.make, nil)
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	ff.waitN(t, 1)
	<-ff.get(0).started
	waitState(t, m, "a", StateRunning)
	if err := m.Stop("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", StateIdle)
}

func TestManagerStartMissingTask(t *testing.T) {
	m := NewManager(testCfg(1), (&fakeFactory{}).make, nil)
	if err := m.Start("ghost"); err == nil {
		t.Fatal("启动不存在的任务应报错")
	}
}

func (e *eventLog) msgsFor(want State) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for i, s := range e.states {
		if s == want {
			out = append(out, e.msgs[i])
		}
	}
	return out
}

func (e *eventLog) waitMsgCount(t *testing.T, want State, n int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m := e.msgsFor(want); len(m) >= n {
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待 %d 条 %s 事件超时，实得 %v", n, want, e.msgsFor(want))
	return nil
}

// 稳定推流数小时后再断流，应从最小退避重新起步，而不是延用上一轮升到的高值
// （规格 §4.4「2s 起步」）。退避值写入事件消息，测试对消息断言，避免计时抖动。
func TestManagerBackoffResetsAfterStableRun(t *testing.T) {
	ff := &fakeFactory{}
	log := &eventLog{}
	m := NewManager(testCfg(2, "a"), ff.make, log.record)
	m.newBackoff = func() *Backoff {
		return &Backoff{Min: 10 * time.Millisecond, Max: 80 * time.Millisecond}
	}
	m.stableAfter = 20 * time.Millisecond
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}

	// 第一轮：瞬间失败（未达稳定阈值）→ 退避 = Min
	ff.waitN(t, 1)
	<-ff.get(0).started
	ff.get(0).exit <- ExitInfo{Err: errors.New("断流")}
	log.waitMsgCount(t, StateReconnecting, 1)

	// 第二轮：稳定运行超过阈值后失败 → 退避应被重置回 Min
	ff.waitN(t, 2)
	<-ff.get(1).started
	time.Sleep(40 * time.Millisecond)
	ff.get(1).exit <- ExitInfo{Err: errors.New("再次断流")}
	msgs := log.waitMsgCount(t, StateReconnecting, 2)

	for i, want := range []string{"10ms", "10ms"} {
		if !strings.Contains(msgs[i], want) {
			t.Fatalf("第 %d 次重连退避应为 %s，实际消息: %q", i+1, want, msgs[i])
		}
	}
}

// Start 必须在持锁状态下同步占位。否则 Start 返回后到 supervise 真正跑起来
// 之间状态仍是 idle，此窗口内的第二次 Start 会为同一任务拉起第二条管道，
// 且 cancels/runners 被覆盖后其中一条永远无法停止（孤儿 ffmpeg 持续占用资源）。
func TestManagerDoubleStartRejected(t *testing.T) {
	ff := &fakeFactory{}
	m := NewManager(testCfg(2, "a"), ff.make, nil)
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	if s := m.State("a"); s == StateIdle {
		t.Fatal("Start 返回后状态仍为 idle：占位未同步完成")
	}
	if err := m.Start("a"); err == nil {
		t.Fatal("重复启动应被拒绝")
	}
	ff.waitN(t, 1)
	<-ff.get(0).started
	waitState(t, m, "a", StateRunning)
	ff.mu.Lock()
	n := len(ff.fakes)
	ff.mu.Unlock()
	if n != 1 {
		t.Fatalf("同一任务应只创建 1 个 runner，实得 %d", n)
	}
}

// 停止后立即重启：旧 supervise 尚在收尾时新一轮已注册，
// 旧协程的清理不得把新一轮的句柄删掉，否则新任务变成停不掉的孤儿。
func TestManagerStopThenRestartKeepsNewRunControllable(t *testing.T) {
	ff := &fakeFactory{}
	m := NewManager(testCfg(2, "a"), ff.make, nil)
	for round := 0; round < 3; round++ {
		if err := m.Start("a"); err != nil {
			t.Fatalf("第 %d 轮启动: %v", round, err)
		}
		ff.waitN(t, round+1)
		<-ff.get(round).started
		waitState(t, m, "a", StateRunning)
		if err := m.Stop("a"); err != nil {
			t.Fatalf("第 %d 轮停止: %v", round, err)
		}
		waitState(t, m, "a", StateIdle)
	}
	if got := m.Running(); got != 0 {
		t.Fatalf("全部停止后运行计数应归零，实得 %d", got)
	}
}

func TestSetMonitoringThenStart(t *testing.T) {
	// 无人值守探到开播就是从探测态直接进推流，这条路必须通
	ff := &fakeFactory{}
	m := NewManager(testCfg(2, "a"), ff.make, nil)

	if err := m.SetMonitoring("a"); err != nil {
		t.Fatalf("进入探测态失败: %v", err)
	}
	if got := m.State("a"); got != StateMonitoring {
		t.Fatalf("State = %s, 期望 monitoring", got)
	}
	if err := m.Start("a"); err != nil {
		t.Fatalf("探测态应能启动: %v", err)
	}
	waitState(t, m, "a", StateRunning)
	_ = m.Stop("a")
	waitState(t, m, "a", StateIdle)
}

func TestSetMonitoringIsIdempotent(t *testing.T) {
	// 探测服务每轮都会调它。若每次都走一遍迁移，monitoring→monitoring
	// 这个非法迁移会往事件日志里灌满"忽略非法迁移"告警
	var events []Event
	var mu sync.Mutex
	ff := &fakeFactory{}
	m := NewManager(testCfg(2, "a"), ff.make, func(ev Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})

	for i := 0; i < 5; i++ {
		if err := m.SetMonitoring("a"); err != nil {
			t.Fatalf("第 %d 次进入探测态失败: %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Errorf("重复进入探测态应只发一条事件，实际 %d 条: %+v", len(events), events)
	}
	for _, ev := range events {
		if strings.Contains(ev.Msg, "非法迁移") {
			t.Errorf("不应出现非法迁移告警: %q", ev.Msg)
		}
	}
}

func TestSetMonitoringRejectedWhileRunning(t *testing.T) {
	ff := &fakeFactory{}
	m := NewManager(testCfg(2, "a"), ff.make, nil)
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", StateRunning)

	if err := m.SetMonitoring("a"); err == nil {
		t.Error("推流中的任务不应能被改成探测态")
	}
	_ = m.Stop("a")
	waitState(t, m, "a", StateIdle)
}
