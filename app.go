package main

import (
	"context"
	"runtime/debug"
	"sync"
	"time"

	"github.com/imbalaomao/liverelay/internal/appcore"
	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/paths"
	"github.com/imbalaomao/liverelay/internal/power"
	"github.com/imbalaomao/liverelay/internal/tools"
	"github.com/imbalaomao/liverelay/internal/tray"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// version 由构建时注入（-ldflags "-X main.version=..."）；开发构建回落到模块信息。
var version = ""

// pushEvent 是推给前端的事件名。
const pushEvent = "tasks:changed"

// flushInterval 决定节流期间攒下的变化多久补推一次。
// 比 appcore.PushInterval 稍长，避免两者打架。
const flushInterval = 400 * time.Millisecond

// EnvInfo 是前端启动时需要的运行环境快照。
type EnvInfo struct {
	Version string `json:"version"`
	Mode    string `json:"mode"`
	DataDir string `json:"dataDir"`
}

// App 是 Wails 绑定层。这里只做参数转发与窗口操作，
// 业务逻辑全在 internal/appcore —— 绑定层跑不起单元测试。
type App struct {
	ctx     context.Context
	dataDir string
	mode    paths.Mode

	icon  []byte
	core  *appcore.Core
	tray  *tray.Service
	power *power.Manager

	stopFlush context.CancelFunc

	// quitting 标记"用户已明确要求退出"。
	//
	// Wails 的 runtime.Quit() 内部会再跑一遍 OnBeforeClose；不区分这两种来源的话，
	// 托盘菜单里的「退出」会被"开了收托盘就隐藏"的策略拦下，变成又一次收进托盘。
	// 只在主线程/回调里读写，但托盘回调来自另一条 goroutine，所以要加锁。
	quitMu   sync.Mutex
	quitting bool
}

func NewApp(icon []byte) *App { return &App{icon: icon} }

// startup 在窗口创建后调用：判定数据根、载入配置、拉起所有后台组件。
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	root, mode, err := paths.Root()
	if err != nil {
		// 定位不到 exe 属于极端环境问题，退回当前目录下的 data/ 以保证可用
		root, mode = "data", paths.Portable
	}
	a.dataDir, a.mode = root, mode

	cfg, err := config.Load(paths.ConfigFile(root))
	if err != nil {
		runtimeLogError(ctx, "配置载入失败，已回退默认配置: "+err.Error())
		cfg = config.Default()
	}

	c, err := appcore.New(root, cfg)
	if err != nil {
		runtimeLogError(ctx, "初始化失败: "+err.Error())
		return
	}
	a.core = c
	a.core.OnLog = func(msg string) { runtimeLogError(ctx, msg) }
	a.core.OnPush = a.push

	a.power = power.New()
	a.power.OnError = func(err error) {
		// 设不上必须让用户知道，否则"我明明开了不休眠"会变成查不出原因的断流
		runtimeLogError(ctx, "阻止休眠失败: "+err.Error())
	}

	a.tray = tray.New(a.icon, a.ShowWindow, a.Quit)
	a.tray.Start()

	fctx, cancel := context.WithCancel(context.Background())
	a.stopFlush = cancel
	go a.flushLoop(fctx)

	a.push(a.core.TaskViews())
}

// flushLoop 把节流窗口内攒下的变化补推给前端。
// 没有它的话，一串密集事件的最后一条会被压住，界面会停在倒数第二个状态上。
func (a *App) flushLoop(ctx context.Context) {
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.core.FlushPush()
		}
	}
}

// push 把最新任务视图发给前端，并顺带更新托盘提示与休眠抑制。
func (a *App) push(views []appcore.TaskView) {
	if a.ctx == nil {
		return
	}
	wruntime.EventsEmit(a.ctx, pushEvent, views)

	n := a.runningTasks()
	if a.tray != nil {
		a.tray.SetStatus(n)
	}
	if a.power != nil && a.core != nil {
		a.power.Update(n, a.core.Settings().PreventSleep)
	}
}

// beforeClose 接管窗口关闭按钮。返回 true 表示阻止关闭。
func (a *App) beforeClose(ctx context.Context) bool {
	if a.isQuitting() {
		// 这一趟是 Quit() 自己触发的回调，放行，别再拿收托盘策略拦自己
		return false
	}
	closeToTray := true
	if a.core != nil {
		closeToTray = a.core.Settings().CloseToTray
	}
	switch tray.OnCloseRequested(closeToTray, a.runningTasks()) {
	case tray.ActionHide:
		wruntime.WindowHide(ctx)
		return true
	case tray.ActionConfirmQuit:
		sel, err := wruntime.MessageDialog(ctx, wruntime.MessageDialogOptions{
			Type:          wruntime.QuestionDialog,
			Title:         "仍在推流",
			Message:       "还有任务正在推流，退出会立即中断。确定要退出吗？",
			Buttons:       []string{"退出", "取消"},
			DefaultButton: "取消",
			CancelButton:  "取消",
		})
		if err != nil {
			// 弹不出对话框时按"别退"处理，宁可多留一个窗口也不要静默掐断直播
			return true
		}
		return sel != "退出"
	default:
		return false
	}
}

// shutdown 在应用退出前收尾，负责归还系统资源。
func (a *App) shutdown(ctx context.Context) {
	if a.stopFlush != nil {
		a.stopFlush()
	}
	if a.core != nil {
		a.core.Close()
	}
	if a.tray != nil {
		a.tray.Stop()
	}
	if a.power != nil {
		// 不归还的话，系统会一直被按着不许睡
		a.power.Close()
	}
}

func (a *App) runningTasks() int {
	if a.core == nil {
		return 0
	}
	return a.core.RunningCount()
}

// ---------- 窗口 ----------

// ShowWindow 从托盘恢复主界面。
func (a *App) ShowWindow() {
	if a.ctx == nil {
		return
	}
	wruntime.WindowShow(a.ctx)
	wruntime.WindowUnminimise(a.ctx)
}

// MinimiseWindow 最小化。自绘标题栏要用。
func (a *App) MinimiseWindow() {
	if a.ctx != nil {
		wruntime.WindowMinimise(a.ctx)
	}
}

// ToggleMaximise 在最大化与还原之间切换。
func (a *App) ToggleMaximise() {
	if a.ctx != nil {
		wruntime.WindowToggleMaximise(a.ctx)
	}
}

// CloseWindow 走与点关闭按钮相同的策略。自绘标题栏的叉调用它。
func (a *App) CloseWindow() {
	if a.ctx == nil {
		return
	}
	if a.beforeClose(a.ctx) {
		return // 已收进托盘或用户取消了退出
	}
	// 策略判定已经放行，置上标志再退——否则 Quit 内部那次 OnBeforeClose
	// 会重新走一遍判定（还会把"仍在推流"的确认框弹第二次）
	a.markQuitting()
	wruntime.Quit(a.ctx)
}

// Quit 退出应用。托盘菜单的「退出」走这里。
func (a *App) Quit() {
	if a.ctx == nil {
		return
	}
	a.markQuitting()
	wruntime.Quit(a.ctx)
}

func (a *App) markQuitting() {
	a.quitMu.Lock()
	a.quitting = true
	a.quitMu.Unlock()
}

func (a *App) isQuitting() bool {
	a.quitMu.Lock()
	defer a.quitMu.Unlock()
	return a.quitting
}

// ---------- 环境 ----------

// Env 返回运行环境快照，供 UI 状态栏显示。
func (a *App) Env() EnvInfo {
	return EnvInfo{Version: appVersion(), Mode: a.mode.Display(), DataDir: a.dataDir}
}

func appVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// ---------- 任务 ----------

func (a *App) Tasks() []appcore.TaskView { return a.core.TaskViews() }

func (a *App) TaskForm(id string) config.Task {
	t, _ := a.core.TaskForm(id)
	return t
}

func (a *App) AddTask(t config.Task) (config.Task, error) {
	got, err := a.core.AddTask(t)
	a.pushNow()
	return got, err
}

func (a *App) UpdateTask(t config.Task) (config.Task, error) {
	got, err := a.core.UpdateTask(t)
	a.pushNow()
	return got, err
}

func (a *App) DeleteTask(id string) error {
	err := a.core.DeleteTask(id)
	a.pushNow()
	return err
}

func (a *App) StartTask(id string) error { return a.core.StartTask(id) }
func (a *App) StopTask(id string) error  { return a.core.StopTask(id) }

func (a *App) TaskEvents(id string) []appcore.EventView { return a.core.Events(id) }

// pushNow 在用户主动改动后立即刷新界面，不等节流窗口。
func (a *App) pushNow() {
	if a.core != nil {
		a.push(a.core.TaskViews())
	}
}

// ---------- 内核 ----------

func (a *App) Tools() []appcore.ToolView { return a.core.ToolViews() }

func (a *App) AddTool(t config.Tool) error  { return a.core.AddTool(t) }
func (a *App) EditTool(t config.Tool) error { return a.core.EditTool(t) }
func (a *App) DeleteTool(id string) error   { return a.core.DeleteTool(id) }

func (a *App) SetToolPath(id, path string) error { return a.core.SetToolPath(id, path) }
func (a *App) ResetToolPath(id string) error     { return a.core.ResetToolPath(id) }

func (a *App) ProbeTool(id string) (tools.Info, error) {
	return a.core.ProbeTool(a.ctx, id)
}

func (a *App) CheckToolUpdate(id string) (appcore.ReleaseView, error) {
	return a.core.CheckToolUpdate(a.ctx, id)
}

func (a *App) UpgradeTool(id string) (appcore.ReleaseView, error) {
	return a.core.UpgradeTool(a.ctx, id)
}

// PickExecutable 弹出文件选择框，返回用户选中的可执行文件路径。
// 取消选择返回空串，不算错误。
func (a *App) PickExecutable() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "选择内核可执行文件",
		Filters: []wruntime.FileFilter{
			{DisplayName: "可执行文件 (*.exe)", Pattern: "*.exe"},
			{DisplayName: "全部文件", Pattern: "*.*"},
		},
	})
}

// PickCookieFile 弹出文件选择框，用于选 Netscape 格式的 cookies.txt。
func (a *App) PickCookieFile() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "选择 YouTube cookies.txt",
		Filters: []wruntime.FileFilter{
			{DisplayName: "Cookies 文件 (*.txt)", Pattern: "*.txt"},
			{DisplayName: "全部文件", Pattern: "*.*"},
		},
	})
}

// PickDirectory 弹出目录选择框，用于选录制目录。
func (a *App) PickDirectory() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "选择录制目录"})
}

// ---------- 设置 ----------

func (a *App) Settings() config.Settings { return a.core.Settings() }

func (a *App) SaveSettings(s config.Settings) error {
	err := a.core.SaveSettings(s)
	a.pushNow()
	return err
}

// ---------- 微博 ----------

func (a *App) WeiboState() appcore.WeiboView { return a.core.WeiboView() }

func (a *App) SaveWeiboCookie(cookie string) (appcore.WeiboView, error) {
	return a.core.SaveWeiboCookie(a.ctx, cookie)
}

func (a *App) ClearWeiboCookie() error { return a.core.ClearWeiboCookie() }

func (a *App) CheckWeiboCookie() appcore.WeiboView {
	return a.core.CheckWeiboCookie(a.ctx)
}
