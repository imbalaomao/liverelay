# LiveRelay

Windows 桌面端的自动直播转发工具：用 streamlink / yt-dlp 等内核抓取直播源，经 ffmpeg 转推到一个或多个目标（RTMP / RTMPS / SRT / UDP / 本地 HLS），支持无人值守开播探测、同步录制与微博直播一键推流。

Go + Wails v2 + Vue 3，单文件 exe，无需安装运行时。

## 界面

`docs/prototype/index.html` 是设计原型，可直接用浏览器打开查看。

## 运行

### 需要准备

- Windows 10 1809 及以上，已安装 [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/)（Win11 自带）
- 三个内核，放在数据目录的 `tools/` 下（也可在界面上指定本地已有的可执行文件）：
  - `tools/streamlink/bin/streamlink.exe` —— 官方 Windows 便携包整包解压，它依赖同目录下的内嵌 Python，只取 exe 是跑不起来的
  - `tools/yt-dlp.exe`
  - `tools/ffmpeg.exe`

内核也可以在「内核」页点「检查更新」直接下载。

### 数据目录

按以下顺序判定，第一个命中的生效：

1. 环境变量 `LIVERELAY_DATA` 指定的目录
2. exe 同级存在 `data/` → 便携模式，一切数据都在这个目录里，删掉即卸载
3. 否则 → `%APPDATA%\LiveRelay`

## 从源码构建

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0
wails build            # 产物在 build/bin/LiveRelay.exe
wails dev              # 带热重载的开发模式
```

国内网络需要先设置 Go 代理：

```bash
go env -w GOPROXY=https://goproxy.cn,direct
```

## 开发

```bash
go test ./...                          # 全部单元测试
CGO_ENABLED=1 go test ./... -race      # 竞态检测（需要 gcc）
go vet ./... && gofmt -l .             # 静态检查

LIVERELAY_NET_TEST=1 go test ./internal/updater/ -run RealGitHub -v   # 联网校验更新源
```

`build/*.ps1` 是几个真机验证脚本（托盘行为、数据目录判定、界面截图），需要用 PowerShell 7 运行——Windows PowerShell 5.1 会把无 BOM 的脚本按 ANSI 读，中文注释被打乱后会连带吞掉后面的语句。

## 目录

| 包 | 职责 |
| --- | --- |
| `internal/config` | 配置读写，原子落盘 + 损坏时回退备份 |
| `internal/core` | 任务状态机、并发上限排队、退避重连 |
| `internal/pipeline` | 抓流进程 → OS 管道 → ffmpeg，含参数模板与日志脱敏 |
| `internal/tools` | 内核注册表、版本与能力探测 |
| `internal/monitor` | 无人值守开播探测 |
| `internal/updater` | 内核在线更新（GitHub Releases + SHA256 校验） |
| `internal/weibo` | 微博直播推流地址获取，cookie 经 DPAPI 加密存本地 |
| `internal/power` | 推流时阻止系统休眠 |
| `internal/tray` | 托盘图标与「关闭到托盘」策略 |
| `internal/paths` | 数据目录判定与标准布局 |
| `internal/appcore` | 绑定层背后的服务编排（可测部分） |
| `app.go` | Wails 绑定层，只做转发 |

## 安全

- 微博 cookie 用 Windows DPAPI 加密后单独存放，不进 `config.json`，也不出现在日志里
- 推流密钥不回传给界面，编辑表单留空即保持原值
- 事件日志经脱敏后才展示，ffmpeg 吐出的完整命令行不会泄漏密钥
- 界面禁用 `v-html`，CSP 不含 `unsafe-inline`，不加载任何远程资源
- 更新包校验 SHA256，解压有 zip-slip 与体积上限防护
