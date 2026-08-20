package main

import (
	"context"
	"runtime/debug"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/paths"
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
}

func NewApp() *App { return &App{} }

// startup 在窗口创建后调用：判定数据根、建立目录布局、载入配置。
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
}

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
