package core

import (
	"context"
	"errors"
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
}

func (e *eventLog) record(ev Event) {
	e.mu.Lock()
	e.states = append(e.states, ev.State)
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
