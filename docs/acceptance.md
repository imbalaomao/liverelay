# 验收报告

记录发布前的安全与资源验收结果。数据来自开发机（Ryzen 9700X / 32GB / NVMe），
目标机（i7-7700HQ / 8GB / SATA SSD）上的数值会有出入，尤其是 WebView2 的缓存策略
会随可用内存变化。

## 自动化验收

以下断言都在 `go test ./...` 里，每次跑都会执行，不依赖人工检查：

| 检查项 | 位置 |
| --- | --- |
| 生产 CSP 严格（`script-src 'self'`，无 `unsafe-inline`/`unsafe-eval`，无 localhost 残留） | `security_test.go` |
| 前端零 `v-html` / `innerHTML` / `eval` / `new Function` | `security_test.go` |
| 打包产物无远程资源加载 | `security_test.go` |
| Core 反复开关不泄漏 goroutine | `internal/appcore/leak_test.go` |
| 8 万条事件后堆增长受控 | `internal/appcore/resource_test.go` |
| 视图重建 2000 次无残留引用 | `internal/appcore/resource_test.go` |
| 内核默认路径与更新器落地位置一致 | `internal/updater/consistency_test.go` |

全仓 427 个用例，`-race -count=2` 全绿。

## XSS 实测

往任务名与源地址里塞入注入载荷，构建后截图确认渲染结果：

```
<img src=x onerror="alert('XSS')">
"><script>document.title='pwned'</script>
{{constructor.constructor('alert(3)')()}}
```

三个载荷全部以纯文本渲染，`onerror` 未触发，窗口标题仍是 `LiveRelay`
（说明 `document.title='pwned'` 没有执行）。Vue 的插值自动转义与 CSP 双重生效。

## 静态扫描（gosec）

```
Files: 34   Lines: 5772   Issues: 27
HIGH: 0   MEDIUM: 10   LOW: 17
```

修复过的实质问题：

- **G115 整数溢出**：`uint32(len(b))` 交给 DPAPI 时若超长会静默截断，
  意味着只加密了开头一小段、剩下的悄悄丢掉，用户直到解密才发现 cookie 是残的。
  已加 1MiB 上限，超长当场拒绝。
- **目录权限**：数据根及其子目录由 0755 收紧到 0700（内含加密凭据与录像），
  内核目录 0750，换入的可执行文件 0700。

剩余 10 条 MEDIUM 全部是 G304「路径来自变量的文件读取」，路径都由数据根拼上常量
文件名得到，数据根是用户为自己这台机器选定的目录，没有外部可控的路径片段。
已在各处以 `#nosec` 加注理由，而不是关掉规则——关掉规则会让以后真正的问题也被淹掉。

同类的 G204「以变量启动子进程」也已加注：拉起用户指定的内核正是本程序的用途，
参数以数组逐个传递、不拼接命令行、全程不经 shell。

## 资源实测

### 空载

| 项 | 数值 |
| --- | --- |
| Go 主进程 | 32 MB |
| 进程树合计 | 335.9 MB（7 个进程） |
| Go 侧线程 | 21 |

其中 WebView2 的 browser / gpu / renderer / utility 进程占了约 300MB，属于 Chromium
的固定开销。已确认无法通过 `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` 调整——Wails 恒会
传入非空的 additionalBrowserArguments，按 WebView2 的规则此时环境变量被忽略。

### 并发推流

2 路真实直播源（B 站）并发推流 30 秒：

| 项 | 数值 |
| --- | --- |
| Go 侧堆峰值 | **0.8 MB** |
| 收尾后 goroutine | 1 |
| 每路接收数据 | 7.1 MB |

堆峰值不到 1MB，印证了 OS 管道零拷贝的设计：抓流进程的 stdout 直接接到 ffmpeg 的
stdin，Go 从头到尾不碰流数据，内存不随路数或时长增长。

## 端到端

| 链路 | 结果 |
| --- | --- |
| 真实直播源 → streamlink → OS 管道 → ffmpeg → RTMP | 25 秒收到 3.2 MB |
| 微博接口取推流地址与观看链接 | 打通，地址每次调用都会变（故不缓存） |
| 微博 cookie DPAPI 加密落盘 | 落盘文件中搜不到明文 |
| 关闭到托盘 / 直接退出 | `build/verify-tray.ps1`、`build/verify-quit.ps1` |
| 便携模式与 `LIVERELAY_DATA` | `build/verify-datadir.ps1` |

## 已知限制

- Twitch 在国内网络下无法直连（`gql.twitch.tv` 超时），需配置代理后使用。
- ffmpeg 的上游用的是滚动标签 `latest`，比不出新旧，界面会如实显示"判断不了"
  而不是谎报有更新。
- 目标机上的内存数值需要复测：WebView2 会按可用内存调整缓存，8GB 机器上应低于此处。
