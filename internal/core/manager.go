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

// Manager 调度全部推流任务：并发上限排队、异常退避重连、优雅停止。
type Manager struct {
	mu         sync.Mutex
	cfg        *config.Config
	factory    RunnerFactory
	onEvent    func(Event)
	states     map[string]State
	cancels    map[string]context.CancelFunc
	runners    map[string]Runner
	queue      []string
	running    int
	newBackoff func() *Backoff
}

func NewManager(cfg *config.Config, f RunnerFactory, onEvent func(Event)) *Manager {
	return &Manager{
		cfg: cfg, factory: f, onEvent: onEvent,
		states:  map[string]State{},
		cancels: map[string]context.CancelFunc{},
		runners: map[string]Runner{},
		newBackoff: func() *Backoff {
			return &Backoff{Min: 2 * time.Second, Max: 60 * time.Second}
		},
	}
}

func (m *Manager) State(id string) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stateOfLocked(id)
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
	if m.cfg.Settings.MaxConcurrent <= 0 {
		return 4
	}
	return m.cfg.Settings.MaxConcurrent
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
	if m.running >= m.maxConcurrent() {
		m.states[id] = StateQueued
		m.queue = append(m.queue, id)
		m.mu.Unlock()
		m.emit(id, StateQueued, "等待空闲槽位")
		return nil
	}
	m.running++
	m.mu.Unlock()
	go m.supervise(id)
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
	c, ok := m.cancels[id]
	r := m.runners[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("任务 %s 未在运行", id)
	}
	c()
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
// 退出时释放槽位并唤醒队首任务。
func (m *Manager) supervise(id string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancels[id] = cancel
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.cancels, id)
		delete(m.runners, id)
		m.running--
		next := m.popQueueLocked()
		if next != "" {
			m.running++
		}
		m.mu.Unlock()
		if next != "" {
			go m.supervise(next)
		}
	}()

	task, ok := m.findTask(id)
	if !ok {
		m.transition(id, StateFailed, "任务不存在")
		return
	}

	bo := m.newBackoff()
	for {
		if ctx.Err() != nil {
			m.transition(id, StateIdle, "已停止")
			return
		}
		m.transition(id, StateStarting, "启动子进程")
		r := m.factory(task)
		m.mu.Lock()
		m.runners[id] = r
		m.mu.Unlock()
		if err := r.Start(ctx); err != nil {
			m.transition(id, StateFailed, err.Error())
			return
		}
		m.transition(id, StateRunning, "推流中")
		info := r.Wait()
		if ctx.Err() != nil {
			m.transition(id, StateIdle, "已停止")
			return
		}
		if info.Normal {
			m.transition(id, StateIdle, "流正常结束")
			return
		}
		m.transition(id, StateReconnecting, fmt.Sprint(info.Err))
		select {
		case <-ctx.Done():
			m.transition(id, StateIdle, "已停止")
			return
		case <-time.After(bo.Next()):
		}
	}
}
