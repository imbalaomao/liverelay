package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

// 关于 WebView2 内存（资源红线）：实测空载整棵进程树约 330MB，其中 Go 侧仅 27MB，
// 其余是 WebView2 的 browser/gpu/renderer/utility 进程，属于 Chromium 的固定开销。
//
// 已排除的路子：设 WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS 环境变量无效——Wails 恒会传入
// 非空的 additionalBrowserArguments（EnableFraudulentWebsiteDetection 默认 false 会带上
// --disable-features=msSmartScreenProtection），按 WebView2 规则此时环境变量被忽略，
// 且 v2.15.0 未暴露注入任意参数的选项。不要再试。
//
// 仍然有效的手段：① 前端自己控制占用（日志环形缓冲上限、长列表虚拟化）——见 M3-7；
// ② WebviewGpuIsDisabled 可省掉 gpu 进程，但会退回软件合成、抬高 CPU，与红线冲突，故不启用。
// 目标机 8GB 上 Chromium 会按内存压力自行收缩缓存，真实数值须在目标机复测（M4）。

func runtimeLogError(ctx context.Context, msg string) {
	if ctx != nil {
		wruntime.LogError(ctx, msg)
	}
}

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "LiveRelay",
		Width:  1100,
		Height: 720,
		// 目标机是 8GB 内存的笔记本，窗口过小会让任务卡片挤成一团
		MinWidth:  900,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// 自绘标题栏（规格 §5.1）
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 11, G: 13, B: 18, A: 1},
		OnStartup:        app.startup,
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			// XSS 红线：禁用 WebView2 的右键菜单与开发者工具入口，只保留打包内资源
			DisableWindowIcon: false,
		},
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("启动失败:", err.Error())
	}
}
