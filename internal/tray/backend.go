package tray

import "github.com/energye/systray"

// systrayBackend 是 systray 包级 API 的薄封装。
// 这里刻意不放任何逻辑：所有判断都在 Service 里，那部分才有测试覆盖。
type systrayBackend struct{}

// Register 用 RunWithExternalLoop 而非 Run：Run 会独占当前线程的消息循环，
// 与 Wails 自己的消息循环打架。
func (systrayBackend) Register(onReady, onExit func()) (start, end func()) {
	return systray.RunWithExternalLoop(onReady, onExit)
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
