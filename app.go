package main

import (
	"context"
	"runtime/debug"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/paths"
	"github.com/imbalaomao/liverelay/internal/power"
	"github.com/imbalaomao/liverelay/internal/tray"
	"github.com/imbalaomao/liverelay/internal/updater"
	"github.com/imbalaomao/liverelay/internal/weibo"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// version 由构建时注入（-ldflags "-X main.version=..."）；开发构建回落到模块信息。
var version = ""

// EnvInfo 是前端启动时需要的运行环境快照。
type EnvInfo struct {
	Version string `json:"version"`
	Mode    string `json:"mode"`
	DataDir string `json:"dataDir"`
}

// App 是 Wails 绑定层：只做参数校验与转发，业务逻辑留在 internal/ 各包内。
type App struct {
	ctx     context.Context
	dataDir string
	mode    paths.Mode
	cfg     *config.Config

	icon  []byte
	tray  *tray.Service
	power *power.Manager
	weibo *weibo.Service
	// weiboStop 停止微博 cookie 的周期复检循环。
	weiboStop context.CancelFunc
}

func NewApp(icon []byte) *App { return &App{icon: icon} }

// startup 在窗口创建后调用：判定数据根、建立目录布局、载入配置、拉起托盘与电源管理。
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	root, mode, err := paths.Root()
	if err != nil {
		// 定位不到 exe 属于极端环境问题，退回当前目录下的 data/ 以保证可用
		root, mode = "data", paths.Portable
	}
	if err := paths.Ensure(root); err != nil {
		runtimeLogError(ctx, "创建数据目录失败: "+err.Error())
	}
	a.dataDir, a.mode = root, mode

	cfg, err := config.Load(paths.ConfigFile(root))
	if err != nil {
		runtimeLogError(ctx, "配置载入失败，已回退默认配置: "+err.Error())
		cfg = config.Default()
	}
	a.cfg = cfg

	a.power = power.New()
	a.power.OnError = func(err error) {
		// 设不上必须让用户知道，否则"我明明开了不休眠"会变成查不出原因的断流
		runtimeLogError(ctx, "阻止休眠失败: "+err.Error())
	}

	a.tray = tray.New(a.icon, a.ShowWindow, a.Quit)
	a.tray.Start()
	a.tray.SetStatus(a.runningTasks())

	a.startWeibo(cfg)
}

// startWeibo 拉起微博 cookie 的周期复检。
// 复检本身很轻（三天才真正打一次请求），但必须有个活着的循环，
// 否则用户只有在点开播的那一刻才会发现登录早就失效了。
func (a *App) startWeibo(cfg *config.Config) {
	a.weibo = weibo.NewService(a.dataDir)

	p := cfg.Settings.Proxy
	if hc, err := updater.NewClient(p.Enabled, p.Type, p.Host, p.Port, p.Username, p.Password); err == nil {
		a.weibo.UseHTTPClient(hc)
	} else {
		runtimeLogError(a.ctx, "代理设置有误，微博接口将直连: "+err.Error())
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.weiboStop = cancel
	go a.weibo.Run(ctx)
}

// beforeClose 接管窗口关闭按钮。返回 true 表示阻止关闭。
func (a *App) beforeClose(ctx context.Context) bool {
	switch tray.OnCloseRequested(a.cfg.Settings.CloseToTray, a.runningTasks()) {
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
	if a.weiboStop != nil {
		a.weiboStop()
	}
	if a.tray != nil {
		a.tray.Stop()
	}
	if a.power != nil {
		// 不归还的话，系统会一直被按着不许睡
		a.power.Close()
	}
}

// ShowWindow 从托盘恢复主界面。
func (a *App) ShowWindow() {
	if a.ctx == nil {
		return
	}
	wruntime.WindowShow(a.ctx)
	wruntime.WindowUnminimise(a.ctx)
}

// Quit 退出应用。
func (a *App) Quit() {
	if a.ctx == nil {
		return
	}
	wruntime.Quit(a.ctx)
}

// runningTasks 返回当前占用推流槽位的任务数。
// TaskManager 尚未接入绑定层（M3-7），此处先返回 0。
func (a *App) runningTasks() int { return 0 }

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
