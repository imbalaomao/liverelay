package tray

import (
	"strings"
	"sync"
	"testing"
)

// ---------- 关闭行为策略 ----------

func TestOnCloseRequested(t *testing.T) {
	cases := []struct {
		name        string
		closeToTray bool
		running     int
		want        Action
	}{
		{"开了收托盘，推流中", true, 3, ActionHide},
		// 开了收托盘就一律收起来，没有任务在推也一样——用户开这个开关
		// 就是不想每次都重开程序
		{"开了收托盘，空闲", true, 0, ActionHide},
		{"没开收托盘，空闲，直接退出", false, 0, ActionQuit},
		// 没开收托盘又正在推流，直接退等于悄无声息掐断直播，必须先问一句
		{"没开收托盘，推流中，需要确认", false, 1, ActionConfirmQuit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := OnCloseRequested(c.closeToTray, c.running); got != c.want {
				t.Errorf("OnCloseRequested(%v, %d) = %v, 期望 %v", c.closeToTray, c.running, got, c.want)
			}
		})
	}
}

func TestTooltip(t *testing.T) {
	if got := Tooltip(0); !strings.Contains(got, "空闲") {
		t.Errorf("Tooltip(0) = %q", got)
	}
	if got := Tooltip(3); !strings.Contains(got, "3") {
		t.Errorf("Tooltip(3) = %q", got)
	}
	// Windows 托盘提示上限 127 个 UTF-16 字符，超了会被系统截断
	if got := Tooltip(9999); len([]rune(got)) > 60 {
		t.Errorf("Tooltip 过长: %q", got)
	}
}

// ---------- 服务生命周期 ----------

type fakeBackend struct {
	mu        sync.Mutex
	registers int
	icon      []byte
	tooltips  []string
	items     []string
	clicks    map[string]func()
	quits     int
	onReady   func()
	started   int
	ended     int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{clicks: map[string]func(){}}
}

func (f *fakeBackend) Register(onReady, _ func()) (func(), func()) {
	f.mu.Lock()
	f.registers++
	f.onReady = onReady
	f.mu.Unlock()
	return func() { f.mu.Lock(); f.started++; f.mu.Unlock() },
		func() { f.mu.Lock(); f.ended++; f.mu.Unlock() }
}

func (f *fakeBackend) SetIcon(b []byte) {
	f.mu.Lock()
	f.icon = b
	f.mu.Unlock()
}

func (f *fakeBackend) SetTooltip(s string) {
	f.mu.Lock()
	f.tooltips = append(f.tooltips, s)
	f.mu.Unlock()
}

func (f *fakeBackend) AddItem(title, _ string, onClick func()) {
	f.mu.Lock()
	f.items = append(f.items, title)
	f.clicks[title] = onClick
	f.mu.Unlock()
}

func (f *fakeBackend) AddSeparator() {}

func (f *fakeBackend) SetOnClick(fn func()) {
	f.mu.Lock()
	f.clicks["<图标左键>"] = fn
	f.mu.Unlock()
}

func (f *fakeBackend) Quit() {
	f.mu.Lock()
	f.quits++
	f.mu.Unlock()
}

func (f *fakeBackend) fireReady() {
	f.mu.Lock()
	fn := f.onReady
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (f *fakeBackend) click(title string) {
	f.mu.Lock()
	fn := f.clicks[title]
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (f *fakeBackend) tips() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tooltips...)
}

func TestStartBuildsMenu(t *testing.T) {
	be := newFakeBackend()
	s := newWith(be, []byte{1, 2, 3}, nil, nil)
	s.Start()
	be.fireReady()

	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.icon) != 3 {
		t.Error("未设置托盘图标")
	}
	want := []string{"显示主界面", "退出"}
	if len(be.items) != len(want) {
		t.Fatalf("菜单项 = %v, 期望 %v", be.items, want)
	}
	for i, w := range want {
		if be.items[i] != w {
			t.Errorf("菜单项[%d] = %q, 期望 %q", i, be.items[i], w)
		}
	}
}

func TestMenuActionsAreWired(t *testing.T) {
	var shown, quit int
	be := newFakeBackend()
	s := newWith(be, nil, func() { shown++ }, func() { quit++ })
	s.Start()
	be.fireReady()

	be.click("显示主界面")
	be.click("退出")
	// 左键点图标是 Windows 上约定俗成的"打开主界面"
	be.click("<图标左键>")

	if shown != 2 {
		t.Errorf("显示主界面被调用 %d 次，期望 2（菜单项 + 图标左键）", shown)
	}
	if quit != 1 {
		t.Errorf("退出被调用 %d 次，期望 1", quit)
	}
}

func TestStartIsIdempotent(t *testing.T) {
	be := newFakeBackend()
	s := newWith(be, nil, nil, nil)
	s.Start()
	s.Start()
	s.Start()

	be.mu.Lock()
	defer be.mu.Unlock()
	if be.registers != 1 {
		t.Errorf("注册了 %d 次托盘，期望 1 次——重复注册会留下多个图标", be.registers)
	}
}

func TestSetStatusBeforeReadyIsDeferred(t *testing.T) {
	// systray 还没初始化就设提示会打到空指针上
	be := newFakeBackend()
	s := newWith(be, nil, nil, nil)
	s.SetStatus(2)
	s.Start()

	if got := be.tips(); len(got) != 0 {
		t.Fatalf("就绪前不该下发提示，实际 %v", got)
	}
	be.fireReady()

	got := be.tips()
	if len(got) != 1 || !strings.Contains(got[0], "2") {
		t.Errorf("就绪后应补上最新的提示，实际 %v", got)
	}
}

func TestSetStatusAfterReady(t *testing.T) {
	be := newFakeBackend()
	s := newWith(be, nil, nil, nil)
	s.Start()
	be.fireReady()
	s.SetStatus(1)
	s.SetStatus(4)

	got := be.tips()
	if len(got) == 0 || !strings.Contains(got[len(got)-1], "4") {
		t.Errorf("提示未更新到最新，实际 %v", got)
	}
}

func TestSetStatusSkipsUnchanged(t *testing.T) {
	be := newFakeBackend()
	s := newWith(be, nil, nil, nil)
	s.Start()
	be.fireReady()
	for i := 0; i < 5; i++ {
		s.SetStatus(2)
	}
	if got := be.tips(); len(got) != 1 {
		t.Errorf("内容没变时不该重复下发，实际 %d 次: %v", len(got), got)
	}
}

func TestStopIsIdempotentAndSafeBeforeStart(t *testing.T) {
	be := newFakeBackend()
	s := newWith(be, nil, nil, nil)
	s.Stop() // 还没 Start 就 Stop，不能 panic

	s2 := newWith(be, nil, nil, nil)
	s2.Start()
	s2.Stop()
	s2.Stop()

	be.mu.Lock()
	defer be.mu.Unlock()
	if be.quits != 1 {
		t.Errorf("Quit 调用了 %d 次，期望 1 次", be.quits)
	}
}
