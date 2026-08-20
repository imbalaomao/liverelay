package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/imbalaomao/liverelay/internal/config"
)

// Event 是任务状态变化的通知，供 UI 层订阅。
type Event struct {
	TaskID string
	State  State
	Msg    string
	At     time.Time
}

// run 是任务的一轮监督生命周期句柄。用指针作身份标识：停止后立即重启时，
// 旧一轮的清理必须只删自己的句柄，否则会误删新一轮的 cancel/runner，
// 让新任务变成停不掉的孤儿进程（持续占用 CPU 与带宽）。
type run struct {
	cancel context.CancelFunc
	runner Runner // 受 Manager.mu 保护
}

// Manager 调度全部推流任务：并发上限排队、异常退避重连、优雅停止。
type Manager struct {
	mu      sync.Mutex
	cfg     *config.Config
	factory RunnerFactory
	onEvent func(Event)
	states  map[string]State
	runs    map[string]*run
	queue   []string
	running int

	newBackoff func() *Backoff
	// stableAfter 是"本轮推流算稳定"的时长门槛：超过它才重置退避。
	// 若一启动就重置，进程反复秒退时将永远按最小间隔重启（抖动风暴）。
	stableAfter time.Duration
}

func NewManager(cfg *config.Config, f RunnerFactory, onEvent func(Event)) *Manager {
	return &Manager{
		cfg: cfg, factory: f, onEvent: onEvent,
		states: map[string]State{},
		runs:   map[string]*run{},
		newBackoff: func() *Backoff {
			return &Backoff{Min: 2 * time.Second, Max: 60 * time.Second}
		},
		stableAfter: 60 * time.Second,
	}
}

func (m *Manager) State(id string) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stateOfLocked(id)
}

// Running 返回当前占用槽位的任务数（含启动中与重连中）。
func (m *Manager) Running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func (m *Manager) stateOfLocked(id string) State {
	if s, ok := m.states[id]; ok {
		return s
	}
	return StateIdle
}

func (m *Manager) emit(id string, s State, msg string) {
	if m.onEvent != nil {
		m.onEvent(Event{TaskID: id, State: s, Msg: msg, At: time.Now()})
	}
}

func (m *Manager) transition(id string, to State, msg string) {
	m.mu.Lock()
	from := m.stateOfLocked(id)
	next, err := Transition(from, to)
	if err != nil {
		m.mu.Unlock()
		m.emit(id, from, fmt.Sprintf("忽略非法迁移 %s→%s: %s", from, to, msg))
		return
	}
	m.states[id] = next
	m.mu.Unlock()
	m.emit(id, next, msg)
}

func (m *Manager) findTask(id string) (config.Task, bool) {
	for _, t := range m.cfg.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return config.Task{}, false
}

func (m *Manager) maxConcurrent() int {
	n := m.cfg.Settings.MaxConcurrent
	if n <= 0 {
		return 4
	}
	if n > config.MaxConcurrentCap {
		return config.MaxConcurrentCap
	}
	return n
}

// claimLocked 在持锁状态下同步占位：置为"启动中"、占用槽位、登记句柄。
// 必须与 Start 在同一临界区内完成——否则 Start 返回后到 supervise 真正跑起来
// 之间状态仍是 idle，此窗口内的第二次 Start 会为同一任务拉起第二条管道。
func (m *Manager) claimLocked(id string) (context.Context, *run) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &run{cancel: cancel}
	m.runs[id] = h
	m.states[id] = StateStarting
	m.running++
	return ctx, h
}

// Start 启动任务；超出并发上限时进入排队，有空闲槽位后自动启动。
func (m *Manager) Start(id string) error {
	if _, ok := m.findTask(id); !ok {
		return fmt.Errorf("任务 %s 不存在", id)
	}
	m.mu.Lock()
	st := m.stateOfLocked(id)
	if st != StateIdle && st != StateFailed {
		m.mu.Unlock()
		return fmt.Errorf("任务 %s 当前状态 %s，无法启动", id, st)
	}
	if _, busy := m.runs[id]; busy {
		m.mu.Unlock()
		return fmt.Errorf("任务 %s 上一轮尚未收尾，请稍后重试", id)
	}
	if m.running >= m.maxConcurrent() {
		m.states[id] = StateQueued
		m.queue = append(m.queue, id)
		m.mu.Unlock()
		m.emit(id, StateQueued, "等待空闲槽位")
		return nil
	}
	ctx, h := m.claimLocked(id)
	m.mu.Unlock()
	m.emit(id, StateStarting, "启动子进程")
	go m.supervise(id, ctx, h)
	return nil
}

// Stop 停止运行中的任务，或将排队中的任务出队。
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	if m.stateOfLocked(id) == StateQueued {
		m.removeFromQueueLocked(id)
		m.states[id] = StateIdle
		m.mu.Unlock()
		m.emit(id, StateIdle, "已出队")
		return nil
	}
	h, ok := m.runs[id]
	var r Runner
	if ok {
		r = h.runner
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("任务 %s 未在运行", id)
	}
	h.cancel()
	if r != nil {
		_ = r.Stop()
	}
	return nil
}

func (m *Manager) removeFromQueueLocked(id string) {
	for i, q := range m.queue {
		if q == id {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return
		}
	}
}

func (m *Manager) popQueueLocked() string {
	if len(m.queue) == 0 {
		return ""
	}
	id := m.queue[0]
	m.queue = m.queue[1:]
	return id
}

// supervise 运行任务的完整生命周期：启动子进程、等待退出、按需退避重连。
// 退出时释放槽位并唤醒队首任务。状态已由调用方置为"启动中"。
func (m *Manager) supervise(id string, ctx context.Context, h *run) {
	// 无论从哪条路径返回都必须释放 context，否则子进程绑定的 watch goroutine 不退出。
	defer h.cancel()

	// 终态迁移必须发生在句柄释放之后：否则用户看到"已停止"时句柄仍在，
	// 紧接着的重启会被"上一轮尚未收尾"拒绝。
	var final State
	var finalMsg string
	defer func() {
		m.mu.Lock()
		if m.runs[id] == h { // 只清理自己这一轮，避免误删已重启的新句柄
			delete(m.runs, id)
		}
		m.running--
		next := m.popQueueLocked()
		var nctx context.Context
		var nh *run
		if next != "" {
			nctx, nh = m.claimLocked(next)
		}
		m.mu.Unlock()
		if final != "" {
			m.transition(id, final, finalMsg)
		}
		if next != "" {
			m.emit(next, StateStarting, "队列轮转，启动子进程")
			go m.supervise(next, nctx, nh)
		}
	}()

	task, ok := m.findTask(id)
	if !ok {
		final, finalMsg = StateFailed, "任务不存在"
		return
	}

	bo := m.newBackoff()
	for first := true; ; first = false {
		if ctx.Err() != nil {
			final, finalMsg = StateIdle, "已停止"
			return
		}
		if !first { // 首轮的"启动中"已由 Start / 队列轮转同步置好
			m.transition(id, StateStarting, "重连启动子进程")
		}
		r := m.factory(task)
		m.mu.Lock()
		h.runner = r
		m.mu.Unlock()
		if err := r.Start(ctx); err != nil {
			final, finalMsg = StateFailed, err.Error()
			return
		}
		m.transition(id, StateRunning, "推流中")
		startedAt := time.Now()
		info := r.Wait()
		if ctx.Err() != nil {
			final, finalMsg = StateIdle, "已停止"
			return
		}
		if info.Normal {
			final, finalMsg = StateIdle, "流正常结束"
			return
		}
		// 本轮撑过了稳定门槛，说明不是连环秒退，退避重新从 Min 起步。
		if time.Since(startedAt) >= m.stableAfter {
			bo.Reset()
		}
		delay := bo.Next()
		m.transition(id, StateReconnecting, fmt.Sprintf("%v；%v 后重连", info.Err, delay))
		select {
		case <-ctx.Done():
			final, finalMsg = StateIdle, "已停止"
			return
		case <-time.After(delay):
		}
	}
}
