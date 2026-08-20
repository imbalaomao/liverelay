package appcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/core"
)

func newTestCore(t *testing.T) *Core {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	c, err := New(dir, cfg)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// ---------- 任务增删改 ----------

func TestAddTaskAssignsID(t *testing.T) {
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{
		Name: "测试", SourceURL: "https://x/live", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a/b", Key: "k"}},
	})
	if err != nil {
		t.Fatalf("AddTask 失败: %v", err)
	}
	if got.ID == "" {
		t.Error("新任务应自动分配 ID")
	}
	if len(c.Tasks()) != 1 {
		t.Errorf("任务数 = %d", len(c.Tasks()))
	}
}

func TestAddTaskIDsAreUnique(t *testing.T) {
	// 用时间戳当 ID 时，连续两次新建会撞在同一毫秒上
	c := newTestCore(t)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		got, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
			Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a/b"}}})
		if err != nil {
			t.Fatal(err)
		}
		if seen[got.ID] {
			t.Fatalf("第 %d 个任务的 ID 与之前重复: %q", i, got.ID)
		}
		seen[got.ID] = true
	}
}

func TestAddTaskValidates(t *testing.T) {
	c := newTestCore(t)
	cases := []struct {
		name string
		task config.Task
	}{
		{"没有名称", config.Task{SourceURL: "https://x", ToolID: "streamlink",
			Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}}},
		{"没有源地址", config.Task{Name: "t", ToolID: "streamlink",
			Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}}},
		{"内核不存在", config.Task{Name: "t", SourceURL: "https://x", ToolID: "并不存在",
			Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}}},
		{"没有推流目标", config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink"}},
		{"目标地址为空", config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
			Targets: []config.Target{{Proto: "rtmp", URL: "  "}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.AddTask(tc.task); err == nil {
				t.Error("应校验失败")
			}
		})
	}
}

func TestWeiboTaskNeedsNoManualTarget(t *testing.T) {
	// 开了微博直播就有推流去处了，不该再强迫用户手填一条
	c := newTestCore(t)
	if _, err := c.AddTask(config.Task{
		Name: "微博", SourceURL: "https://x/live", ToolID: "streamlink", WeiboLive: true,
	}); err != nil {
		t.Errorf("微博直播任务不该被要求手填目标: %v", err)
	}
}

func TestUpdateTask(t *testing.T) {
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "旧名", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}})
	if err != nil {
		t.Fatal(err)
	}
	got.Name = "新名"
	if _, err := c.UpdateTask(got); err != nil {
		t.Fatalf("UpdateTask 失败: %v", err)
	}
	if c.Tasks()[0].Name != "新名" {
		t.Errorf("未更新: %+v", c.Tasks()[0])
	}
}

func TestUpdateMissingTask(t *testing.T) {
	c := newTestCore(t)
	_, err := c.UpdateTask(config.Task{ID: "不存在", Name: "t", SourceURL: "https://x",
		ToolID: "streamlink", Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}})
	if err == nil {
		t.Error("改不存在的任务应报错")
	}
}

func TestDeleteTask(t *testing.T) {
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteTask(got.ID); err != nil {
		t.Fatalf("DeleteTask 失败: %v", err)
	}
	if len(c.Tasks()) != 0 {
		t.Error("任务应已删除")
	}
}

// ---------- 落盘 ----------

func TestMutationsPersist(t *testing.T) {
	// 改完不存盘，用户重开程序发现全白干了
	dir := t.TempDir()
	c, err := New(dir, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddTask(config.Task{Name: "持久", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}}); err != nil {
		t.Fatal(err)
	}
	c.Close()

	loaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tasks) != 1 || loaded.Tasks[0].Name != "持久" {
		t.Errorf("配置未落盘: %+v", loaded.Tasks)
	}
}

func TestSettingsPersistAndClamp(t *testing.T) {
	c := newTestCore(t)
	s := c.Settings()
	s.MaxConcurrent = 9999 // 手填一个离谱值
	s.ProbeIntervalSec = 5
	if err := c.SaveSettings(s); err != nil {
		t.Fatalf("SaveSettings 失败: %v", err)
	}
	got := c.Settings()
	if got.MaxConcurrent != config.MaxConcurrentCap {
		t.Errorf("MaxConcurrent = %d，应被钳到 %d", got.MaxConcurrent, config.MaxConcurrentCap)
	}
	if got.ProbeIntervalSec < 30 {
		t.Errorf("ProbeIntervalSec = %d，应被钳到下限以上", got.ProbeIntervalSec)
	}
}

// ---------- 与运行中的任务交互 ----------

func TestDeleteRunningTaskIsRejected(t *testing.T) {
	// 删掉一个正在推的任务，会留下一条没人管的管道
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}})
	if err != nil {
		t.Fatal(err)
	}
	c.mgr.SetConfig(c.snapshot())
	c.states[got.ID] = core.StateRunning // 直接摆布状态，不真起进程

	if err := c.DeleteTask(got.ID); err == nil {
		t.Error("推流中的任务不应能被直接删除")
	}
}

func TestUpdateRunningTaskIsRejected(t *testing.T) {
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}})
	if err != nil {
		t.Fatal(err)
	}
	c.states[got.ID] = core.StateRunning
	got.Name = "改名"
	if _, err := c.UpdateTask(got); err == nil {
		t.Error("推流中的任务不应能被直接编辑——改完的参数不会生效，只会让人以为改了")
	}
}

// ---------- 事件节流 ----------

func TestEventsAreThrottled(t *testing.T) {
	// 重连风暴时事件可以一秒几十条，原样推给 WebView 会把渲染打满
	c := newTestCore(t)
	var pushes int
	var last []TaskView
	c.OnPush = func(v []TaskView) {
		pushes++
		last = v
	}
	now := time.Now()
	c.now = func() time.Time { return now }

	for i := 0; i < 100; i++ {
		c.onEvent(core.Event{TaskID: "a", State: core.StateRunning, Msg: "推流中"})
	}
	if pushes > 1 {
		t.Errorf("节流窗口内推送了 %d 次，期望至多 1 次", pushes)
	}

	now = now.Add(PushInterval + time.Millisecond)
	c.onEvent(core.Event{TaskID: "a", State: core.StateRunning, Msg: "推流中"})
	if pushes != 2 {
		t.Errorf("超过节流窗口后应再推一次，实际累计 %d 次", pushes)
	}
	_ = last
}

func TestEventLogIsBounded(t *testing.T) {
	// 通宵重连会攒下几万条事件，无上限地留着就是内存泄漏
	c := newTestCore(t)
	for i := 0; i < MaxEventsPerTask*3; i++ {
		c.onEvent(core.Event{TaskID: "a", State: core.StateReconnecting, Msg: "断流重连"})
	}
	if n := len(c.Events("a")); n > MaxEventsPerTask {
		t.Errorf("事件条数 = %d，超过上限 %d", n, MaxEventsPerTask)
	}
}

func TestEventLogRedactsSecrets(t *testing.T) {
	// ffmpeg 报错时会把完整命令行吐出来，推流密钥就在里面
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a/b", Key: "SUPERSECRETKEY"}}})
	if err != nil {
		t.Fatal(err)
	}
	c.onEvent(core.Event{TaskID: got.ID, State: core.StateFailed,
		Msg: "ffmpeg 退出: rtmp://a/b/SUPERSECRETKEY 拒绝连接"})

	events := c.Events(got.ID)
	if len(events) == 0 {
		t.Fatal("事件未记录")
	}
	for _, ev := range events {
		if strings.Contains(ev.Msg, "SUPERSECRETKEY") {
			t.Errorf("事件日志泄漏了推流密钥: %q", ev.Msg)
		}
	}
}

// ---------- 视图 ----------

func TestTaskViewCarriesState(t *testing.T) {
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "看板", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a", Key: "k"}}})
	if err != nil {
		t.Fatal(err)
	}
	views := c.TaskViews()
	if len(views) != 1 {
		t.Fatalf("视图数 = %d", len(views))
	}
	v := views[0]
	if v.ID != got.ID || v.Name != "看板" {
		t.Errorf("视图内容不对: %+v", v)
	}
	if v.State != string(core.StateIdle) {
		t.Errorf("State = %q, 期望 idle", v.State)
	}
	if v.ToolName != "streamlink" {
		t.Errorf("ToolName = %q", v.ToolName)
	}
}

func TestTaskViewNeverCarriesStreamKey(t *testing.T) {
	// 视图会整个序列化给前端，推流密钥不能跟着出去
	c := newTestCore(t)
	if _, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a/b", Key: "SUPERSECRETKEY"}}}); err != nil {
		t.Fatal(err)
	}
	for _, v := range c.TaskViews() {
		for _, tg := range v.Targets {
			if strings.Contains(tg, "SUPERSECRETKEY") {
				t.Errorf("任务视图泄漏了推流密钥: %q", tg)
			}
		}
	}
}

// ---------- 生命周期 ----------

func TestCloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	c.Close()
}

func TestNewCreatesLayout(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, sub := range []string{"tools", "logs", "recordings", "cache"} {
		if _, err := os.Stat(filepath.Join(dir, sub)); err != nil {
			t.Errorf("缺少目录 %s", sub)
		}
	}
}

func TestStartMissingTask(t *testing.T) {
	c := newTestCore(t)
	if err := c.StartTask("不存在"); err == nil {
		t.Error("启动不存在的任务应报错")
	}
}

func TestStopNotRunningTask(t *testing.T) {
	c := newTestCore(t)
	if err := c.StopTask("不存在"); err == nil {
		t.Error("停止未运行的任务应报错")
	}
}

// ---------- 编辑表单与密钥 ----------

func TestTaskFormBlanksStreamKey(t *testing.T) {
	// 编辑表单要能显示已有目标，但密钥不能回传进 WebView
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a/b", Key: "SUPERSECRETKEY"}}})
	if err != nil {
		t.Fatal(err)
	}
	form, ok := c.TaskForm(got.ID)
	if !ok {
		t.Fatal("找不到任务")
	}
	if form.Targets[0].URL != "rtmp://a/b" {
		t.Errorf("地址应照常显示: %q", form.Targets[0].URL)
	}
	if form.Targets[0].Key != "" {
		t.Errorf("密钥不该回传: %q", form.Targets[0].Key)
	}
	// 但要让界面知道"这里原本是有密钥的"，好显示成已设置而不是空
	if !form.Targets[0].HasKey {
		t.Error("应标记该目标原本设有密钥")
	}
}

func TestUpdateTaskKeepsKeyWhenFormLeavesItBlank(t *testing.T) {
	// 表单里密钥是空的（因为我们没回传），保存时不能把原密钥抹掉
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a/b", Key: "ORIGINALKEY"}}})
	if err != nil {
		t.Fatal(err)
	}
	form, _ := c.TaskForm(got.ID)
	form.Name = "改了名"
	if _, err := c.UpdateTask(form); err != nil {
		t.Fatalf("UpdateTask 失败: %v", err)
	}

	for _, tk := range c.Tasks() {
		if tk.ID == got.ID {
			if tk.Targets[0].Key != "ORIGINALKEY" {
				t.Errorf("原密钥被抹掉了: %q", tk.Targets[0].Key)
			}
			return
		}
	}
	t.Fatal("任务不见了")
}

func TestUpdateTaskAcceptsNewKey(t *testing.T) {
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a/b", Key: "OLDKEY"}}})
	if err != nil {
		t.Fatal(err)
	}
	form, _ := c.TaskForm(got.ID)
	form.Targets[0].Key = "NEWKEY"
	if _, err := c.UpdateTask(form); err != nil {
		t.Fatal(err)
	}
	for _, tk := range c.Tasks() {
		if tk.ID == got.ID && tk.Targets[0].Key != "NEWKEY" {
			t.Errorf("新密钥未生效: %q", tk.Targets[0].Key)
		}
	}
}

func TestUpdateTaskDoesNotKeepKeyForChangedURL(t *testing.T) {
	// 地址换了就是换了个推流点，旧密钥留着只会推失败还查不出原因
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a/b", Key: "OLDKEY"}}})
	if err != nil {
		t.Fatal(err)
	}
	form, _ := c.TaskForm(got.ID)
	form.Targets[0].URL = "rtmp://完全不同的地址/x"
	if _, err := c.UpdateTask(form); err != nil {
		t.Fatal(err)
	}
	for _, tk := range c.Tasks() {
		if tk.ID == got.ID && tk.Targets[0].Key != "" {
			t.Errorf("换了地址还留着旧密钥: %q", tk.Targets[0].Key)
		}
	}
}

func TestTaskFormMissing(t *testing.T) {
	c := newTestCore(t)
	if _, ok := c.TaskForm("不存在"); ok {
		t.Error("不存在的任务应返回 false")
	}
}

func TestAddTaskDropsFormOnlyField(t *testing.T) {
	// HasKey 只是表单回显用的标记，不该跟着落进配置文件
	c := newTestCore(t)
	if _, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a", Key: "k", HasKey: true}}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(c.dataDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hasKey") {
		t.Errorf("配置文件里出现了表单专用字段:\n%s", raw)
	}
}
