// Package appcore 是绑定层背后的服务编排：把配置、任务调度、无人值守探测、
// 内核管理、更新器与微博直播接在一起，向 UI 暴露一组不含机密的视图。
//
// 单独成包是为了能测：Wails 的绑定层跑不起单元测试，而这里的编排逻辑
// （校验、落盘、状态汇总、事件节流、脱敏）恰恰是最该被测的部分。
package appcore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/core"
	"github.com/imbalaomao/liverelay/internal/monitor"
	"github.com/imbalaomao/liverelay/internal/paths"
	"github.com/imbalaomao/liverelay/internal/pipeline"
	"github.com/imbalaomao/liverelay/internal/tools"
	"github.com/imbalaomao/liverelay/internal/updater"
	"github.com/imbalaomao/liverelay/internal/weibo"
)

// PushInterval 是推送给前端的最小间隔。重连风暴时事件可以一秒几十条，
// 原样转给 WebView 会把渲染打满——目标机只有一块核显。
const PushInterval = 250 * time.Millisecond

// MaxEventsPerTask 是每个任务保留的事件条数上限。
// 通宵重连能攒下几万条，无上限地留着就是内存泄漏（内存红线）。
const MaxEventsPerTask = 200

// EventView 是给 UI 看的一条事件。
type EventView struct {
	State string `json:"state"`
	Msg   string `json:"msg"`
	At    string `json:"at"`
}

// TaskView 是任务卡片需要的一切。
//
// 有意不直接复用 config.Task：那里面有推流密钥，而这个结构会被整个
// 序列化丢给 WebView。密钥只以"协议 + 主机"的形式出现。
type TaskView struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	SourceURL  string   `json:"sourceUrl"`
	State      string   `json:"state"`
	StateText  string   `json:"stateText"`
	ToolID     string   `json:"toolId"`
	ToolName   string   `json:"toolName"`
	Quality    string   `json:"quality"`
	Targets    []string `json:"targets"`
	Unattended bool     `json:"unattended"`
	AutoRecord bool     `json:"autoRecord"`
	WeiboLive  bool     `json:"weiboLive"`
	WatchURL   string   `json:"watchUrl"`
	LastMsg    string   `json:"lastMsg"`
}

// Core 编排所有后台组件。
type Core struct {
	// OnPush 在任务视图发生变化时被调用（已节流）。
	OnPush func([]TaskView)
	// OnLog 用于把内部错误交给宿主记录。
	OnLog func(string)

	dataDir string

	mu     sync.Mutex
	cfg    *config.Config
	states map[string]core.State
	events map[string][]EventView
	// lastMsg 记录每个任务最近一条事件文案，任务卡片直接显示它。
	lastMsg map[string]string
	// seq 保证同一毫秒内连续新建的任务不会撞 ID。
	seq int

	mgr      *core.Manager
	mon      *monitor.Service
	weibo    *weibo.Service
	resolver *weibo.Resolver
	up       *updater.Updater
	prober   *tools.Prober

	// now 与 lastPush 用于推送节流。
	now      func() time.Time
	lastPush time.Time
	dirty    bool

	stop   context.CancelFunc
	closed bool
	wg     sync.WaitGroup
}

// New 建立目录布局并把各组件接起来。cfg 由调用方载入。
func New(dataDir string, cfg *config.Config) (*Core, error) {
	if err := paths.Ensure(dataDir); err != nil {
		return nil, err
	}
	c := &Core{
		dataDir: dataDir,
		cfg:     cfg,
		states:  map[string]core.State{},
		events:  map[string][]EventView{},
		lastMsg: map[string]string{},
		now:     time.Now,
		prober:  &tools.Prober{},
	}

	c.weibo = weibo.NewService(dataDir)
	c.resolver = weibo.NewResolver(c.weibo)
	c.up = &updater.Updater{InUse: c.toolInUse}
	c.applyProxy(cfg)

	c.mgr = core.NewManager(cfg, c.newRunner, c.onEvent)
	c.mgr.Prepare = c.resolver.Prepare
	c.mon = monitor.New(cfg, dataDir, c.mgr, c.onEvent)

	ctx, cancel := context.WithCancel(context.Background())
	c.stop = cancel
	c.wg.Add(2)
	go func() { defer c.wg.Done(); c.mon.Run(ctx) }()
	go func() { defer c.wg.Done(); c.weibo.Run(ctx) }()
	return c, nil
}

// Close 停掉后台循环并等待在途探测收尾。可重复调用。
func (c *Core) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()

	if c.stop != nil {
		c.stop()
	}
	c.wg.Wait()
	// 探测子进程必须等干净，否则退出后会留下游离的 python 进程
	c.mon.Wait()
	_ = c.save()
}

// applyProxy 让更新器与微博接口都走用户配置的代理。
func (c *Core) applyProxy(cfg *config.Config) {
	p := cfg.Settings.Proxy
	hc, err := updater.NewClient(p.Enabled, p.Type, p.Host, p.Port, p.Username, p.Password)
	if err != nil {
		c.logf("代理设置有误，相关请求将直连: %v", err)
		return
	}
	c.up.Client = hc
	c.weibo.UseHTTPClient(hc)
}

func (c *Core) logf(format string, args ...any) {
	if c.OnLog != nil {
		c.OnLog(fmt.Sprintf(format, args...))
	}
}

// snapshot 返回当前配置指针。调用方只读。
func (c *Core) snapshot() *config.Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

// save 把配置写盘。
func (c *Core) save() error {
	return config.Save(paths.ConfigFile(c.dataDir), c.snapshot())
}

// commit 把改动后的配置同步给各组件并落盘。
// 三件事必须一起做：漏掉任何一件都会让"界面上改了、实际没生效"。
func (c *Core) commit() error {
	cfg := c.snapshot()
	c.mgr.SetConfig(cfg)
	c.mon.SetConfig(cfg)
	return c.save()
}

// newRunner 是 core.RunnerFactory。
func (c *Core) newRunner(t config.Task) core.Runner {
	cfg := c.snapshot()
	tool, ok := tools.Find(cfg, t.ToolID)
	if !ok {
		// 返回一个必定启动失败的 Runner，让失败原因走正常的事件通道
		return failedRunner{fmt.Errorf("内核 %s 不存在", t.ToolID)}
	}
	ffmpeg, ok := tools.Find(cfg, "ffmpeg")
	if !ok {
		return failedRunner{fmt.Errorf("缺少 ffmpeg 内核")}
	}
	return pipeline.NewRunner(pipeline.Options{
		Task:       t,
		FetchTool:  withResolvedPath(c.dataDir, tool),
		FFmpegPath: tools.Resolved(c.dataDir, ffmpeg),
		DataDir:    c.dataDir,
		Record:     t.AutoRecord,
		CookieFile: cfg.Settings.YouTubeCookieFile,
	})
}

// withResolvedPath 把内核的相对路径锚定到数据根后再交给 Runner。
func withResolvedPath(dataDir string, t config.Tool) config.Tool {
	t.Path = tools.Resolved(dataDir, t)
	t.PathOverride = ""
	return t
}

// failedRunner 用来把"还没开始就注定失败"的原因送进正常的状态机。
type failedRunner struct{ err error }

func (f failedRunner) Start(context.Context) error { return f.err }
func (f failedRunner) Wait() core.ExitInfo         { return core.ExitInfo{Err: f.err} }
func (f failedRunner) Stop() error                 { return nil }

// toolInUse 报告某个内核是否正被运行中的任务占用，供更新器判断能否换文件。
func (c *Core) toolInUse(toolID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.cfg.Tasks {
		if t.ToolID != toolID && t.RecordToolID != toolID {
			continue
		}
		switch c.states[t.ID] {
		case core.StateRunning, core.StateStarting, core.StateReconnecting, core.StateQueued:
			return true
		}
	}
	// ffmpeg 是所有推流的出口，只要有任务在跑就不能动它
	if toolID == "ffmpeg" {
		for _, st := range c.states {
			switch st {
			case core.StateRunning, core.StateStarting, core.StateReconnecting, core.StateQueued:
				return true
			}
		}
	}
	return false
}

// secretsLocked 收集需要在日志里抹掉的串。
func (c *Core) secretsLocked() []string {
	var out []string
	for _, t := range c.cfg.Tasks {
		for _, tg := range t.Targets {
			if tg.Key != "" {
				out = append(out, tg.Key)
			}
		}
	}
	return append(out, c.resolver.Secrets()...)
}

// onEvent 接收来自 Manager 与 monitor 的状态事件。
func (c *Core) onEvent(ev core.Event) {
	c.mu.Lock()
	c.states[ev.TaskID] = ev.State

	msg := pipeline.Redact(ev.Msg, c.secretsLocked())
	if msg != "" {
		c.lastMsg[ev.TaskID] = msg
		at := ev.At
		if at.IsZero() {
			at = c.now()
		}
		list := append(c.events[ev.TaskID], EventView{
			State: string(ev.State), Msg: msg, At: at.Format("15:04:05"),
		})
		// 只保留最近的一段：通宵重连能攒下几万条
		if len(list) > MaxEventsPerTask {
			list = list[len(list)-MaxEventsPerTask:]
		}
		c.events[ev.TaskID] = list
	}
	c.mu.Unlock()

	c.schedulePush()
}

// schedulePush 按 PushInterval 节流地把最新视图推给前端。
func (c *Core) schedulePush() {
	if c.OnPush == nil {
		return
	}
	c.mu.Lock()
	now := c.now()
	if now.Sub(c.lastPush) < PushInterval {
		// 窗口内的变化先攒着，等下一次推送时一并带出去
		c.dirty = true
		c.mu.Unlock()
		return
	}
	c.lastPush = now
	c.dirty = false
	c.mu.Unlock()

	c.OnPush(c.TaskViews())
}

// FlushPush 把节流期间攒下的变化推出去，由宿主定时调用。
func (c *Core) FlushPush() {
	if c.OnPush == nil {
		return
	}
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return
	}
	c.dirty = false
	c.lastPush = c.now()
	c.mu.Unlock()

	c.OnPush(c.TaskViews())
}

// Events 返回某个任务的事件日志（已脱敏）。
func (c *Core) Events(taskID string) []EventView {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 空数组而不是 nil：nil 到了前端是 null，v-for 会当场报错
	out := make([]EventView, 0, len(c.events[taskID]))
	return append(out, c.events[taskID]...)
}

// Tasks 返回任务列表副本。
func (c *Core) Tasks() []config.Task {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]config.Task(nil), c.cfg.Tasks...)
}

// Settings 返回设置副本。
func (c *Core) Settings() config.Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.Settings
}

// SaveSettings 保存设置。数值会被钳到合法区间。
func (c *Core) SaveSettings(s config.Settings) error {
	c.mu.Lock()
	if s.MaxConcurrent <= 0 {
		s.MaxConcurrent = 4
	}
	if s.MaxConcurrent > config.MaxConcurrentCap {
		s.MaxConcurrent = config.MaxConcurrentCap
	}
	if s.ProbeIntervalSec < 30 {
		s.ProbeIntervalSec = 60
	}
	if s.ProbeIntervalSec > 300 {
		s.ProbeIntervalSec = 300
	}
	next := *c.cfg
	next.Settings = s
	c.cfg = &next
	c.mu.Unlock()

	c.applyProxy(c.snapshot())
	return c.commit()
}

// TaskViews 汇总任务列表 + 运行状态，供 UI 直接渲染。
func (c *Core) TaskViews() []TaskView {
	c.mu.Lock()
	tasks := append([]config.Task(nil), c.cfg.Tasks...)
	cfg := c.cfg
	states := make(map[string]core.State, len(c.states))
	for k, v := range c.states {
		states[k] = v
	}
	msgs := make(map[string]string, len(c.lastMsg))
	for k, v := range c.lastMsg {
		msgs[k] = v
	}
	c.mu.Unlock()

	out := make([]TaskView, 0, len(tasks))
	for _, t := range tasks {
		st := states[t.ID]
		if st == "" {
			st = core.StateIdle
		}
		toolName := t.ToolID
		if tool, ok := tools.Find(cfg, t.ToolID); ok {
			toolName = tool.Name
		}
		out = append(out, TaskView{
			ID: t.ID, Name: t.Name, SourceURL: t.SourceURL,
			State: string(st), StateText: stateText(st),
			ToolID: t.ToolID, ToolName: toolName, Quality: t.Quality,
			Targets:    targetLabels(t),
			Unattended: t.Unattended, AutoRecord: t.AutoRecord, WeiboLive: t.WeiboLive,
			WatchURL: c.resolver.WatchURL(t.ID),
			LastMsg:  msgs[t.ID],
		})
	}
	return out
}

// targetLabels 把推流目标压成"协议 · 主机"的短标签。
// 绝不能带上密钥：这个结构会被整个序列化丢进 WebView。
func targetLabels(t config.Task) []string {
	out := make([]string, 0, len(t.Targets)+1)
	for _, tg := range t.Targets {
		out = append(out, strings.ToUpper(tg.Proto)+" · "+hostOf(tg.URL))
	}
	if t.WeiboLive {
		out = append(out, "微博直播")
	}
	return out
}

func hostOf(raw string) string {
	s := raw
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return raw
	}
	return s
}

func stateText(s core.State) string {
	switch s {
	case core.StateRunning:
		return "推流中"
	case core.StateStarting:
		return "启动中"
	case core.StateQueued:
		return "排队中"
	case core.StateReconnecting:
		return "重连中"
	case core.StateMonitoring:
		return "探测中"
	case core.StateFailed:
		return "失败"
	default:
		return "空闲"
	}
}

// stateOf 取任务当前状态。
func (c *Core) stateOf(id string) core.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st, ok := c.states[id]; ok {
		return st
	}
	return core.StateIdle
}

// busy 报告任务是否正占着推流槽位。
func busy(s core.State) bool {
	switch s {
	case core.StateRunning, core.StateStarting, core.StateReconnecting, core.StateQueued:
		return true
	}
	return false
}

// validateTask 校验用户填的任务。
func (c *Core) validateTask(t config.Task) error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("请填写任务名称")
	}
	if strings.TrimSpace(t.SourceURL) == "" {
		return fmt.Errorf("请填写直播源地址")
	}
	cfg := c.snapshot()
	tool, ok := tools.Find(cfg, t.ToolID)
	if !ok {
		return fmt.Errorf("抓流内核 %q 不存在", t.ToolID)
	}
	if tools.NeedsYouTubeCookies(tool, t.SourceURL, cfg.Settings.YouTubeCookieFile) {
		return fmt.Errorf("%s", tools.YouTubeCookieHint())
	}
	if t.RecordToolID != "" {
		if _, ok := tools.Find(cfg, t.RecordToolID); !ok {
			return fmt.Errorf("录制内核 %q 不存在", t.RecordToolID)
		}
	}
	for _, tg := range t.Targets {
		if strings.TrimSpace(tg.URL) == "" {
			return fmt.Errorf("推流目标的地址不能为空")
		}
	}
	// 开了微博直播就有去处了，不必再强迫用户手填一条
	if len(t.Targets) == 0 && !t.WeiboLive {
		return fmt.Errorf("至少要有一个推流目标")
	}
	if t.CustomArgs != "" {
		if _, err := pipeline.Tokenize(t.CustomArgs); err != nil {
			return err
		}
	}
	return nil
}

// nextID 生成任务 ID。带自增序号，避免同一毫秒内连续新建撞 ID。
func (c *Core) nextID() string {
	c.seq++
	return "t" + strconv.FormatInt(c.now().UnixMilli(), 36) + "-" + strconv.Itoa(c.seq)
}

// AddTask 新建任务。
func (c *Core) AddTask(t config.Task) (config.Task, error) {
	if err := c.validateTask(t); err != nil {
		return config.Task{}, err
	}
	c.mu.Lock()
	t.ID = c.nextID()
	// HasKey 只是表单回显用的标记，不能跟着落进配置文件
	for i := range t.Targets {
		t.Targets[i].HasKey = false
	}
	next := *c.cfg
	next.Tasks = append(append([]config.Task(nil), c.cfg.Tasks...), t)
	c.cfg = &next
	c.mu.Unlock()

	return t, c.commit()
}

// TaskForm 返回供编辑表单使用的任务副本。
//
// 推流密钥被抹掉：这个结构会整个序列化进 WebView，密钥没有理由跑到那边去。
// 但会置上 HasKey，好让界面显示成"已设置"而不是一个空框。
func (c *Core) TaskForm(id string) (config.Task, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.cfg.Tasks {
		if t.ID != id {
			continue
		}
		out := t
		out.Targets = make([]config.Target, len(t.Targets))
		for i, tg := range t.Targets {
			out.Targets[i] = config.Target{
				Proto: tg.Proto, URL: tg.URL, HasKey: tg.Key != "",
			}
		}
		return out, true
	}
	return config.Task{}, false
}

// mergeKeys 把表单里留空的密钥补回原值。
//
// 表单拿到的副本里密钥是空的（我们没回传），直接保存会把用户的密钥抹掉。
// 但只在推流地址没变时才补：地址换了就是换了个推流点，旧密钥留着只会
// 推失败，还查不出原因。
func mergeKeys(old, next config.Task) config.Task {
	byURL := map[string]string{}
	for _, tg := range old.Targets {
		if tg.Key != "" {
			byURL[tg.Proto+"|"+tg.URL] = tg.Key
		}
	}
	out := next
	out.Targets = append([]config.Target(nil), next.Targets...)
	for i := range out.Targets {
		out.Targets[i].HasKey = false // 不落配置文件
		if out.Targets[i].Key != "" {
			continue
		}
		if k, ok := byURL[out.Targets[i].Proto+"|"+out.Targets[i].URL]; ok {
			out.Targets[i].Key = k
		}
	}
	return out
}

// UpdateTask 修改任务。运行中的任务不允许改：改完的参数不会生效，
// 只会让人以为改了。
func (c *Core) UpdateTask(t config.Task) (config.Task, error) {
	if err := c.validateTask(t); err != nil {
		return config.Task{}, err
	}
	if busy(c.stateOf(t.ID)) {
		return config.Task{}, fmt.Errorf("任务正在运行，请先停止再编辑")
	}

	c.mu.Lock()
	idx := -1
	for i := range c.cfg.Tasks {
		if c.cfg.Tasks[i].ID == t.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		c.mu.Unlock()
		return config.Task{}, fmt.Errorf("任务不存在")
	}
	merged := mergeKeys(c.cfg.Tasks[idx], t)
	next := *c.cfg
	next.Tasks = append([]config.Task(nil), c.cfg.Tasks...)
	next.Tasks[idx] = merged
	c.cfg = &next
	c.mu.Unlock()

	return merged, c.commit()
}

// DeleteTask 删除任务。运行中的任务不允许删，否则会留下一条没人管的管道。
func (c *Core) DeleteTask(id string) error {
	if busy(c.stateOf(id)) {
		return fmt.Errorf("任务正在运行，请先停止再删除")
	}
	c.mu.Lock()
	idx := -1
	for i := range c.cfg.Tasks {
		if c.cfg.Tasks[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		c.mu.Unlock()
		return fmt.Errorf("任务不存在")
	}
	next := *c.cfg
	next.Tasks = append(append([]config.Task(nil), c.cfg.Tasks[:idx]...), c.cfg.Tasks[idx+1:]...)
	c.cfg = &next
	delete(c.states, id)
	delete(c.events, id)
	delete(c.lastMsg, id)
	c.mu.Unlock()

	return c.commit()
}

// StartTask 手动开播。
func (c *Core) StartTask(id string) error {
	// 配置可能是手改的，cookie 文件也可能事后被删，开播前再确认一次。
	// 让任务起来后卡在一句看不懂的 yt-dlp 报错上，比直接拦下来糟糕得多。
	if err := c.checkYouTube(id); err != nil {
		return err
	}
	return c.mgr.Start(id)
}

// checkYouTube 在启动前确认 yt-dlp 抓 YouTube 的前提已满足。
func (c *Core) checkYouTube(id string) error {
	cfg := c.snapshot()
	for _, t := range cfg.Tasks {
		if t.ID != id {
			continue
		}
		tool, ok := tools.Find(cfg, t.ToolID)
		if ok && tools.NeedsYouTubeCookies(tool, t.SourceURL, cfg.Settings.YouTubeCookieFile) {
			return fmt.Errorf("%s", tools.YouTubeCookieHint())
		}
	}
	return nil
}

// StopTask 停止任务。
func (c *Core) StopTask(id string) error { return c.mgr.Stop(id) }

// RunningCount 返回占用推流槽位的任务数，供托盘与电源管理使用。
func (c *Core) RunningCount() int { return c.mgr.Running() }
