package tray

import (
	"runtime"

	"github.com/energye/systray"
)

// systrayBackend 是 systray 包级 API 的薄封装。
// 这里刻意不放任何逻辑：所有判断都在 Service 里，那部分才有测试覆盖。
type systrayBackend struct{}

// Register 在一条锁定的专属 OS 线程上跑起 systray。
//
// 这里有个非踩不可的坑：systray 在 Windows 上，注册阶段会在**调用它的那个线程**
// 上创建一个隐藏窗口，而消息泵调的是 GetMessage —— 后者只能取到调用线程自己
// 名下窗口的消息。若两者不在同一线程，托盘图标画得出来，但点击、菜单、Quit()
// 全部石沉大海，最后只剩一个既点不动也关不掉的僵尸图标。
//
// systray.RunWithExternalLoop 正是这种分裂用法：它在调用方线程建窗口，却把消息泵
// 丢进另一个 goroutine。改用 systray.Run —— 它把注册与消息泵放在同一个调用线程上，
// 我们再用 LockOSThread 把这条 goroutine 钉死在那根线程上，Go 调度器就搬不走它了。
//
// Run 会一直阻塞到 Quit，所以必须在独立 goroutine 里跑；它占的是自己那根线程，
// 不会和 Wails 的主消息循环抢。
func (systrayBackend) Register(onReady, onExit func()) (start, end func()) {
	started := make(chan struct{})
	start = func() {
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			close(started)
			systray.Run(onReady, onExit)
		}()
		<-started
	}
	// end 只负责让消息循环退出，图标的移除由 Quit 完成
	end = func() {}
	return start, end
}

func (systrayBackend) SetIcon(b []byte) { systray.SetIcon(b) }

func (systrayBackend) SetTooltip(s string) { systray.SetTooltip(s) }

func (systrayBackend) AddItem(title, tooltip string, onClick func()) {
	item := systray.AddMenuItem(title, tooltip)
	item.Click(onClick)
}

func (systrayBackend) AddSeparator() { systray.AddSeparator() }

func (systrayBackend) SetOnClick(fn func()) {
	systray.SetOnClick(func(systray.IMenu) { fn() })
}

func (systrayBackend) Quit() { systray.Quit() }
