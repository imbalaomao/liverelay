// Package tray 提供托盘图标与"关闭到托盘"的行为策略。
package tray

import (
	"fmt"
	"sync"
	"time"
)

// Action 是点击窗口关闭按钮后该采取的动作。
type Action int

const (
	// ActionHide 收进托盘，推流不中断。
	ActionHide Action = iota
	// ActionQuit 直接退出。
	ActionQuit
	// ActionConfirmQuit 退出会掐断正在进行的推流，需要先问用户一句。
	ActionConfirmQuit
)

func (a Action) String() string {
	switch a {
	case ActionHide:
		return "收进托盘"
	case ActionConfirmQuit:
		return "确认后退出"
	default:
		return "退出"
	}
}

// OnCloseRequested 决定点关闭按钮时做什么。
//
// 单独抽出来是因为这里最容易出人命：没开"关闭到托盘"又正在推流时直接退出，
// 等于悄无声息地掐断直播——那种时候必须先问一句。
func OnCloseRequested(closeToTray bool, runningTasks int) Action {
	if closeToTray {
		return ActionHide
	}
	if runningTasks > 0 {
		return ActionConfirmQuit
	}
	return ActionQuit
}

// OnQuitRequested 决定用户明确要求退出时做什么（托盘菜单的「退出」）。
//
// 与 OnCloseRequested 的区别至关重要：Wails 的 runtime.Quit() 内部会再跑一遍
// OnBeforeClose，若那时仍按"开了收托盘就隐藏"来判断，退出就会被自己的策略
// 拦下、变成又一次收进托盘——用户看到的就是"退出按钮点了没反应"。
//
// 明确的退出意图必须凌驾于收托盘设置之上。这里连"正在推流要不要确认"都不再问：
// 用户是从托盘菜单里主动选的退出，不是误触窗口的叉。
func OnQuitRequested(closeToTray bool, runningTasks int) Action {
	return ActionQuit
}

// Tooltip 是鼠标悬停在托盘图标上时的提示。
// 收在托盘里时这是用户唯一能看到的状态，所以要一眼看出在不在推。
func Tooltip(running int) string {
	if running <= 0 {
		return "LiveRelay — 空闲"
	}
	return fmt.Sprintf("LiveRelay — %d 路推流中", running)
}

// backend 抽出 systray 的包级 API。systray 全是包级函数且依赖真实的
// Windows 消息循环，抽成接口才能对生命周期与去重逻辑做确定性测试。
type backend interface {
	Register(onReady, onExit func()) (start, end func())
	SetIcon([]byte)
	SetTooltip(string)
	AddItem(title, tooltip string, onClick func())
	AddSeparator()
	SetOnClick(func())
	Quit()
}

// defaultStopTimeout 是等待托盘退出的上限。
// 等是必要的（见 Stop），但不能无限等：systray 万一卡住，关机流程不该跟着挂死。
const defaultStopTimeout = 3 * time.Second

// Service 管理托盘图标的生命周期。
type Service struct {
	// OnLog 用于报告关机时图标是否真的被移除。
	// 僵尸图标只有用户肉眼能发现，没有这条日志就无从排查。
	OnLog func(string)

	be     backend
	icon   []byte
	onShow func()
	onQuit func()

	// stopTimeout 可覆盖，测试用。
	stopTimeout time.Duration
	// exited 在 systray 真正退出（图标已移除）时关闭。
	exited chan struct{}

	mu      sync.Mutex
	started bool
	stopped bool
	ready   bool
	tip     string
	end     func()
}

// New 建一个真实的托盘服务。icon 需要是 .ico 字节。
func New(icon []byte, onShow, onQuit func()) *Service {
	return newWith(systrayBackend{}, icon, onShow, onQuit)
}

func newWith(be backend, icon []byte, onShow, onQuit func()) *Service {
	return &Service{
		be: be, icon: icon, onShow: onShow, onQuit: onQuit,
		stopTimeout: defaultStopTimeout,
		exited:      make(chan struct{}),
	}
}

// Start 注册托盘图标。重复调用是空操作——注册两次会在托盘里留下两个图标。
func (s *Service) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	start, end := s.be.Register(s.onReady, s.onExited)
	s.mu.Lock()
	s.end = end
	s.mu.Unlock()
	start()
}

func (s *Service) onReady() {
	if len(s.icon) > 0 {
		s.be.SetIcon(s.icon)
	}
	s.be.AddItem("显示主界面", "打开 LiveRelay 主窗口", s.fire(s.onShow))
	s.be.AddSeparator()
	s.be.AddItem("退出", "退出 LiveRelay，正在推流的任务会被停止", s.fire(s.onQuit))
	// 左键点图标打开主界面，是 Windows 上约定俗成的行为
	s.be.SetOnClick(s.fire(s.onShow))

	s.mu.Lock()
	s.ready = true
	tip := s.tip
	s.mu.Unlock()
	if tip != "" {
		s.be.SetTooltip(tip)
	}
}

// onExited 由 systray 在图标移除后回调。用 sync.Once 的语义关一次通道即可。
func (s *Service) onExited() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.exited:
	default:
		close(s.exited)
	}
}

func (s *Service) fire(fn func()) func() {
	return func() {
		if fn != nil {
			fn()
		}
	}
}

// SetStatus 更新悬停提示。就绪前调用会暂存，等托盘初始化完成后补上——
// systray 尚未初始化时下发会打到空指针上。
func (s *Service) SetStatus(running int) {
	tip := Tooltip(running)
	s.mu.Lock()
	if tip == s.tip {
		s.mu.Unlock()
		return
	}
	s.tip = tip
	ready := s.ready
	s.mu.Unlock()
	if ready {
		s.be.SetTooltip(tip)
	}
}

// Stop 移除托盘图标并等它真的消失。可重复调用，也可以在 Start 之前调用。
//
// 必须等：systray.Quit() 只是 PostMessage(WM_CLOSE)，发完就返回，图标要等
// 消息循环走到 WM_DESTROY 才被 Shell_NotifyIcon(NIM_DELETE) 移除。不等的话
// 进程会抢先退出，托盘里留下一个鼠标划过才消失的僵尸图标。
// systray 在删除图标后紧接着回调 onExit，等到它就等于确认图标已经没了。
func (s *Service) Stop() {
	s.mu.Lock()
	if s.stopped || !s.started {
		s.stopped = true
		s.mu.Unlock()
		return
	}
	s.stopped = true
	end := s.end
	timeout := s.stopTimeout
	s.mu.Unlock()

	s.be.Quit()
	if end != nil {
		end()
	}

	if timeout <= 0 {
		timeout = defaultStopTimeout
	}
	select {
	case <-s.exited:
		s.logf("托盘图标已移除")
	case <-time.After(timeout):
		// 等不到就放弃：残留一个图标，总好过程序卡在关机流程里关不掉
		s.logf("等待托盘退出超时（%v），图标可能残留至鼠标划过托盘区", timeout)
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.OnLog != nil {
		s.OnLog(fmt.Sprintf(format, args...))
	}
}
