package monitor

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/core"
)

// fakeStarter 记录 Start 调用，并允许测试摆布任务状态。
type fakeStarter struct {
	mu       sync.Mutex
	states   map[string]core.State
	started  []string
	startErr error
}

func newFakeStarter() *fakeStarter {
	return &fakeStarter{states: map[string]core.State{}}
}

func (f *fakeStarter) State(id string) core.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.states[id]; ok {
		return s
	}
	return core.StateIdle
}

func (f *fakeStarter) Start(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, id)
	f.states[id] = core.StateStarting
	return nil
}

func (f *fakeStarter) SetMonitoring(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[id] = core.StateMonitoring
	return nil
}

func (f *fakeStarter) startedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.started...)
}

func (f *fakeStarter) setState(id string, s core.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[id] = s
}

// testService 造一个时钟可控、探测结果可控的 Service。
func testService(t *testing.T, tasks []config.Task, result func(id string) Result) (*Service, *fakeStarter, *time.Time) {
	t.Helper()
	cfg := config.Default()
	cfg.Tasks = tasks
	cfg.Settings.ProbeIntervalSec = 60

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	st := newFakeStarter()
	s := New(cfg, "data", st, nil)
	s.now = func() time.Time { return now }
	s.probe = func(_ context.Context, _ config.Tool, url string) Result {
		return result(url)
	}
	return s, st, &now
}

func unattended(ids ...string) []config.Task {
	var out []config.Task
	for _, id := range ids {
		out = append(out, config.Task{
			ID: id, Name: id, SourceURL: "https://example.com/" + id,
			ToolID: "streamlink", Unattended: true,
		})
	}
	return out
}

func alwaysLive(string) Result    { return Result{Status: Live, Detail: "已开播"} }
func alwaysOffline(string) Result { return Result{Status: Offline, Detail: "未开播"} }

// ---------- 筛选 ----------

func TestSweepOnlyProbesUnattended(t *testing.T) {
	tasks := unattended("a")
	tasks = append(tasks, config.Task{ID: "b", Name: "b", ToolID: "streamlink", Unattended: false})

	s, st, _ := testService(t, tasks, alwaysLive)
	s.sweep(context.Background())
	s.Wait()

	got := st.startedIDs()
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("只应启动无人值守任务，实际 %v", got)
	}
}

func TestSweepSkipsBusyStates(t *testing.T) {
	// 已经在跑的任务不能再探测——探测本身要起一个 python 进程，
	// 对每个正在推流的任务都来一发会白白吃掉目标机的 CPU
	for _, busy := range []core.State{core.StateRunning, core.StateStarting, core.StateQueued, core.StateReconnecting} {
		t.Run(string(busy), func(t *testing.T) {
			s, st, _ := testService(t, unattended("a"), alwaysLive)
			st.setState("a", busy)

			if n := s.sweep(context.Background()); n != 0 {
				t.Errorf("状态 %s 时不应发起探测，实际发起 %d 个", busy, n)
			}
			s.Wait()
		})
	}
}

func TestSweepAcceptsIdleMonitoringFailed(t *testing.T) {
	for _, ok := range []core.State{core.StateIdle, core.StateMonitoring, core.StateFailed} {
		t.Run(string(ok), func(t *testing.T) {
			s, st, _ := testService(t, unattended("a"), alwaysOffline)
			st.setState("a", ok)

			if n := s.sweep(context.Background()); n != 1 {
				t.Errorf("状态 %s 时应发起探测，实际 %d 个", ok, n)
			}
			s.Wait()
		})
	}
}

// ---------- 单飞与并发上限 ----------

func TestSweepIsSingleFlightPerTask(t *testing.T) {
	release := make(chan struct{})
	var probes int
	var mu sync.Mutex

	s, _, _ := testService(t, unattended("a"), nil)
	s.probe = func(context.Context, config.Tool, string) Result {
		mu.Lock()
		probes++
		mu.Unlock()
		<-release
		return Result{Status: Offline}
	}

	s.sweep(context.Background())
	// 上一发还没回来，这一轮不该再对同一任务发起探测
	if n := s.sweep(context.Background()); n != 0 {
		t.Errorf("同一任务的探测在途时不应重复发起，实际 %d 个", n)
	}
	close(release)
	s.Wait()

	mu.Lock()
	defer mu.Unlock()
	if probes != 1 {
		t.Errorf("探测执行了 %d 次，期望 1 次", probes)
	}
}

func TestSweepRespectsConcurrencyCap(t *testing.T) {
	release := make(chan struct{})
	s, _, _ := testService(t, unattended("a", "b", "c", "d", "e"), nil)
	// 全部立即到期，才能验证并发上限确实拦住了后面的
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		s.due[id] = s.now().Add(-time.Minute)
	}
	s.probe = func(context.Context, config.Tool, string) Result {
		<-release
		return Result{Status: Offline}
	}

	n := s.sweep(context.Background())
	if n != maxConcurrentProbes {
		t.Errorf("同时发起 %d 个探测，上限应为 %d", n, maxConcurrentProbes)
	}
	close(release)
	s.Wait()
}

func TestProbeSlotIsReleased(t *testing.T) {
	// 名额用完不还，第二轮就再也探测不动了
	s, _, now := testService(t, unattended("a"), alwaysOffline)
	s.sweep(context.Background())
	s.Wait()

	*now = now.Add(2 * time.Minute)
	if n := s.sweep(context.Background()); n != 1 {
		t.Errorf("探测结束后名额应归还，第二轮实际发起 %d 个", n)
	}
	s.Wait()
}

// ---------- 节流与错峰 ----------

func TestSweepHonoursInterval(t *testing.T) {
	s, _, now := testService(t, unattended("a"), alwaysOffline)
	s.sweep(context.Background())
	s.Wait()

	*now = now.Add(30 * time.Second) // 未到 60s 间隔
	if n := s.sweep(context.Background()); n != 0 {
		t.Errorf("未到探测间隔不应发起，实际 %d 个", n)
	}
	*now = now.Add(31 * time.Second)
	if n := s.sweep(context.Background()); n != 1 {
		t.Errorf("超过间隔后应发起，实际 %d 个", n)
	}
	s.Wait()
}

func TestFirstSweepStaggersTasks(t *testing.T) {
	// 十个无人值守任务若同时开探，会在同一秒拉起十个 python 进程，
	// 目标机只有 8GB —— 必须把首次探测摊开在一个间隔内
	s, _, _ := testService(t, unattended("a", "b", "c", "d", "e", "f", "g", "h", "i", "j"), alwaysOffline)
	n := s.sweep(context.Background())
	s.Wait()

	if n > maxConcurrentProbes {
		t.Errorf("首轮发起 %d 个探测，超过并发上限", n)
	}
	// 到期时刻必须分散，不能全挤在同一时刻
	seen := map[time.Time]int{}
	for _, d := range s.due {
		seen[d]++
	}
	if len(seen) < 5 {
		t.Errorf("首次到期时刻只有 %d 种，未有效错峰: %v", len(seen), s.due)
	}
}

// ---------- 结果处理 ----------

func TestLiveTriggersStart(t *testing.T) {
	s, st, _ := testService(t, unattended("a"), alwaysLive)
	s.sweep(context.Background())
	s.Wait()

	if got := st.startedIDs(); len(got) != 1 || got[0] != "a" {
		t.Errorf("探测到开播应立即启动任务，实际 %v", got)
	}
}

func TestOfflineDoesNotStart(t *testing.T) {
	s, st, _ := testService(t, unattended("a"), alwaysOffline)
	s.sweep(context.Background())
	s.Wait()

	if got := st.startedIDs(); len(got) != 0 {
		t.Errorf("未开播不应启动任务，实际 %v", got)
	}
}

func TestRepeatedOfflineEmitsOnce(t *testing.T) {
	// 每 60 秒往事件日志里塞一条"未开播"，一夜下来就是几百条噪声
	var events []core.Event
	var mu sync.Mutex

	cfg := config.Default()
	cfg.Tasks = unattended("a")
	s := New(cfg, "data", newFakeStarter(), func(ev core.Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	now := time.Now()
	s.now = func() time.Time { return now }
	s.probe = func(context.Context, config.Tool, string) Result {
		return Result{Status: Offline, Detail: "未开播"}
	}

	for i := 0; i < 5; i++ {
		s.sweep(context.Background())
		s.Wait()
		now = now.Add(2 * time.Minute)
	}

	mu.Lock()
	defer mu.Unlock()
	offline := 0
	for _, ev := range events {
		if strings.Contains(ev.Msg, "未开播") {
			offline++
		}
	}
	if offline != 1 {
		t.Errorf("重复的同一结果应只报一次，实际 %d 条", offline)
	}
}

func TestChangedDetailEmitsAgain(t *testing.T) {
	var msgs []string
	var mu sync.Mutex

	cfg := config.Default()
	cfg.Tasks = unattended("a")
	s := New(cfg, "data", newFakeStarter(), func(ev core.Event) {
		mu.Lock()
		msgs = append(msgs, ev.Msg)
		mu.Unlock()
	})
	now := time.Now()
	s.now = func() time.Time { return now }

	details := []string{"未开播", "未开播", "解析失败：网络不通", "解析失败：网络不通", "未开播"}
	for _, d := range details {
		s.probe = func(context.Context, config.Tool, string) Result {
			return Result{Status: Offline, Detail: d}
		}
		s.sweep(context.Background())
		s.Wait()
		now = now.Add(2 * time.Minute)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(msgs) != 3 {
		t.Errorf("结果变化时应重新上报，期望 3 条，实际 %d 条: %v", len(msgs), msgs)
	}
}

func TestMissingToolIsReported(t *testing.T) {
	var msgs []string
	var mu sync.Mutex

	cfg := config.Default()
	cfg.Tasks = []config.Task{{ID: "a", Name: "a", ToolID: "不存在的内核", Unattended: true}}
	s := New(cfg, "data", newFakeStarter(), func(ev core.Event) {
		mu.Lock()
		msgs = append(msgs, ev.Msg)
		mu.Unlock()
	})
	s.sweep(context.Background())
	s.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(msgs) == 0 || !strings.Contains(msgs[0], "内核") {
		t.Errorf("内核缺失应有明确提示，实际 %v", msgs)
	}
}

// ---------- 生命周期 ----------

func TestRunStopsOnContextCancel(t *testing.T) {
	s, _, _ := testService(t, unattended("a"), alwaysOffline)
	s.tick = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run 未在 context 取消后退出——退出时会留下一堆探测子进程")
	}
	s.Wait()
}
