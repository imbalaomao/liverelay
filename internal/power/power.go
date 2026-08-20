// Package power 负责"推流时不让电脑睡着"。
package power

import (
	"runtime"
	"sync"
)

// Manager 维护"是否需要阻止系统休眠"，并把变化下发给操作系统。
//
// 关键约束：Windows 的 SetThreadExecutionState 是**线程级**的，而 Go 的 goroutine
// 会在 OS 线程之间自由迁移。在普通 goroutine 里调用，状态会落在一个随时可能被
// 调度器回收的线程上——设完就没了，用户以为开了不休眠，半夜照样断流。
// 所以这里固定用一条 runtime.LockOSThread 锁定的专属线程来持有该状态。
type Manager struct {
	// OnError 在系统调用失败时回调。设不上必须让用户知道，
	// 否则"我明明开了不休眠"会变成一个查不出原因的断流。
	OnError func(error)

	apply func(keepAwake bool) error

	mu     sync.Mutex
	closed bool
	req    chan bool
	done   chan struct{}
}

// New 建一个下发到真实系统的 Manager。
func New() *Manager { return newWith(setKeepAwake) }

func newWith(apply func(bool) error) *Manager {
	m := &Manager{
		apply: apply,
		req:   make(chan bool, 1),
		done:  make(chan struct{}),
	}
	go m.loop()
	return m
}

// Update 根据"当前有几个任务在推流"和"用户是否开启了该设置"决定是否阻止休眠。
// 策略放在这里而不是散在调用方，是为了只有一处需要测。
func (m *Manager) Update(runningTasks int, enabled bool) {
	m.send(enabled && runningTasks > 0)
}

// Close 释放阻止休眠并停止后台线程。可重复调用。
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.req)
	m.mu.Unlock()
	<-m.done
}

func (m *Manager) send(want bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	// req 是容量 1 的通道：只关心"最新的期望值"，旧的丢掉即可。
	// 任务状态每秒都在变，排队积压毫无意义。
	select {
	case m.req <- want:
	default:
		select {
		case <-m.req:
		default:
		}
		select {
		case m.req <- want:
		default:
		}
	}
}

func (m *Manager) loop() {
	// 这条 goroutine 与它的 OS 线程绑定到底：执行状态就挂在这个线程上。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(m.done)

	current := false
	defer func() {
		// 进程退出前务必归还，否则系统会一直被按着不许睡
		if current {
			m.set(false)
		}
	}()

	for want := range m.req {
		if want == current {
			continue // 状态没变就不去打扰系统
		}
		if m.set(want) {
			current = want
		}
	}
}

// set 下发一次，返回是否成功。
func (m *Manager) set(keepAwake bool) bool {
	if err := m.apply(keepAwake); err != nil {
		if m.OnError != nil {
			m.OnError(err)
		}
		return false
	}
	return true
}
