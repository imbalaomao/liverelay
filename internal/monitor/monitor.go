// Package monitor 实现"无人值守"：周期性探测源站是否开播，一旦开播就把任务交给
// Manager 拉起推流。
package monitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/core"
	"github.com/imbalaomao/liverelay/internal/pipeline"
	"github.com/imbalaomao/liverelay/internal/tools"
)

// maxConcurrentProbes 是同时在跑的探测子进程数上限（资源红线）。
// 每次探测都要拉起一个 streamlink/yt-dlp 进程（Python 打包，启动开销不小），
// 十个无人值守任务同时开探，在 8GB 的目标机上就是一次可感的卡顿。
const maxConcurrentProbes = 2

// probeTimeout 是单次探测的超时，必须小于最小探测间隔 30s，
// 否则上一发还没超时下一发就该来了。
const probeTimeout = 20 * time.Second

// defaultTick 是扫描节拍。到期判断按每个任务各自的下次到期时刻算，
// 这里只决定判断的粒度。
const defaultTick = 5 * time.Second

// Starter 是 monitor 需要 Manager 提供的能力。抽成接口是为了能用假实现做确定性测试。
type Starter interface {
	Start(id string) error
	State(id string) core.State
	SetMonitoring(id string) error
}

// Service 是无人值守探测服务。
type Service struct {
	cfg     *config.Config
	dataDir string
	mgr     Starter
	onEvent func(core.Event)

	// probe 与 now 可注入，测试用。
	probe func(ctx context.Context, tool config.Tool, url string) Result
	now   func() time.Time
	tick  time.Duration

	mu       sync.Mutex
	due      map[string]time.Time
	inflight map[string]struct{}
	lastMsg  map[string]string

	// sem 是全局探测名额。用带缓冲 channel 而非计数器，是为了能非阻塞地试探：
	// 名额满时本轮直接跳过，不排队、不堆积 goroutine。
	sem chan struct{}
	wg  sync.WaitGroup
}

func New(cfg *config.Config, dataDir string, mgr Starter, onEvent func(core.Event)) *Service {
	s := &Service{
		cfg: cfg, dataDir: dataDir, mgr: mgr, onEvent: onEvent,
		now: time.Now, tick: defaultTick,
		due:      map[string]time.Time{},
		inflight: map[string]struct{}{},
		lastMsg:  map[string]string{},
		sem:      make(chan struct{}, maxConcurrentProbes),
	}
	s.probe = s.runTool
	return s
}

// Run 一直扫描到 ctx 取消。返回前不等待在途探测，调用方退出时应再调 Wait。
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweep(ctx)
		}
	}
}

// Wait 等待所有在途探测收尾。退出流程必须调用，否则会留下游离的子进程。
func (s *Service) Wait() { s.wg.Wait() }

// SetConfig 热替换配置。探测循环在后台读 cfg，绑定层同时可能在增删任务，
// 不加保护就是数据竞争。传 nil 是空操作。
func (s *Service) SetConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// config 取当前配置快照。
func (s *Service) config() *config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// interval 取配置的探测间隔。config.parse 已经钳制过范围，
// 这里再兜一次底：配置结构体可能是调用方直接构造的。
func (s *Service) interval() time.Duration {
	n := s.config().Settings.ProbeIntervalSec
	if n < 30 {
		n = 60
	}
	if n > 300 {
		n = 300
	}
	return time.Duration(n) * time.Second
}

// sweep 扫描一轮，返回本轮发起的探测数。
func (s *Service) sweep(ctx context.Context) int {
	eligible := s.eligible()
	if len(eligible) == 0 {
		return 0
	}
	now := s.now()
	interval := s.interval()
	launched := 0

	for i, t := range eligible {
		s.mu.Lock()
		if _, busy := s.inflight[t.ID]; busy {
			// 单飞：上一发还没回来就不再发，否则慢探测会把进程越堆越多
			s.mu.Unlock()
			continue
		}
		if _, ok := s.due[t.ID]; !ok {
			// 首次登记时把到期时刻摊开在一个探测间隔内错峰，
			// 否则所有无人值守任务会在同一秒一起开探
			s.due[t.ID] = now.Add(time.Duration(i) * interval / time.Duration(len(eligible)))
		}
		if now.Before(s.due[t.ID]) {
			s.mu.Unlock()
			continue
		}
		select {
		case s.sem <- struct{}{}:
		default:
			// 名额已满，本轮到此为止，剩下的下一拍再说
			s.mu.Unlock()
			return launched
		}
		s.inflight[t.ID] = struct{}{}
		s.mu.Unlock()

		s.wg.Add(1)
		go s.runProbe(ctx, t)
		launched++
	}
	return launched
}

// eligible 挑出该探测的任务：开了无人值守，且当前没在跑。
func (s *Service) eligible() []config.Task {
	var out []config.Task
	for _, t := range s.config().Tasks {
		if !t.Unattended {
			continue
		}
		switch s.mgr.State(t.ID) {
		case core.StateIdle, core.StateMonitoring, core.StateFailed:
			out = append(out, t)
		}
	}
	return out
}

func (s *Service) runProbe(ctx context.Context, t config.Task) {
	defer s.wg.Done()
	defer func() {
		// interval() 自己要拿 s.mu，必须在进临界区之前算好——
		// Go 的 Mutex 不可重入，在持锁状态下调用它会当场死锁
		next := s.now().Add(s.interval())
		s.mu.Lock()
		delete(s.inflight, t.ID)
		s.due[t.ID] = next
		s.mu.Unlock()
		<-s.sem
	}()

	tool, ok := tools.Find(s.config(), t.ToolID)
	if !ok {
		s.report(t.ID, fmt.Sprintf("内核 %s 不存在，无法探测开播", t.ToolID))
		return
	}
	_ = s.mgr.SetMonitoring(t.ID)

	res := s.probe(ctx, tool, t.SourceURL)
	if res.Status != Live {
		s.report(t.ID, res.Detail)
		return
	}
	if err := s.mgr.Start(t.ID); err != nil {
		s.report(t.ID, "探测到开播但启动失败："+err.Error())
		return
	}
	// 已经开播并接管，下次回到探测态时该重新播报
	s.clearLast(t.ID)
}

// runTool 是真实的探测实现：按内核自己的探测参数模板跑一次，解读输出。
func (s *Service) runTool(ctx context.Context, tool config.Tool, url string) Result {
	if len(tool.ProbeTemplate) == 0 {
		return Result{Status: Unknown,
			Detail: fmt.Sprintf("内核 %s 没有配置开播探测参数，无法用于无人值守", tool.Name)}
	}
	args := pipeline.RenderTemplate(tool.ProbeTemplate, map[string]string{"url": url})
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	// 代理经由继承的环境变量生效，与推流链路走同一套设置
	out, err := tools.RunCapture(cctx, tools.Resolved(s.dataDir, tool), args...)
	return interpret(out, err)
}

// report 上报一次探测结果，内容与上次相同时不再重复上报。
// 没有这层去重，一个任务通宵未开播就会往日志里灌进上千条"未开播"。
func (s *Service) report(id, msg string) {
	if msg == "" {
		return
	}
	s.mu.Lock()
	if s.lastMsg[id] == msg {
		s.mu.Unlock()
		return
	}
	s.lastMsg[id] = msg
	s.mu.Unlock()
	s.emit(id, core.StateMonitoring, msg)
}

func (s *Service) clearLast(id string) {
	s.mu.Lock()
	delete(s.lastMsg, id)
	s.mu.Unlock()
}

func (s *Service) emit(id string, st core.State, msg string) {
	if s.onEvent != nil {
		s.onEvent(core.Event{TaskID: id, State: st, Msg: msg, At: s.now()})
	}
}
