package appcore

import (
	"context"
	"strings"
	"testing"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/core"
)

// ---------- 内核列表 ----------

func TestToolViewsCoverBuiltins(t *testing.T) {
	c := newTestCore(t)
	views := c.ToolViews()
	if len(views) != 3 {
		t.Fatalf("内核数 = %d, 期望 3 个内置", len(views))
	}
	byID := map[string]ToolView{}
	for _, v := range views {
		byID[v.ID] = v
	}
	for _, id := range []string{"streamlink", "yt-dlp", "ffmpeg"} {
		v, ok := byID[id]
		if !ok {
			t.Fatalf("缺少内置内核 %s", id)
		}
		if !v.Builtin {
			t.Errorf("%s 应标记为内置", id)
		}
		// 路径要显示解析后的绝对位置，用户才知道程序到底在用哪个文件
		if !strings.Contains(v.Path, "tools") {
			t.Errorf("%s 的路径未解析: %q", id, v.Path)
		}
		if !v.CanUpdate {
			t.Errorf("%s 应支持在线更新", id)
		}
	}
}

func TestToolViewShowsUsage(t *testing.T) {
	c := newTestCore(t)
	if _, err := c.AddTask(config.Task{Name: "占用者", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}}); err != nil {
		t.Fatal(err)
	}
	for _, v := range c.ToolViews() {
		if v.ID == "streamlink" {
			if len(v.UsedBy) != 1 || v.UsedBy[0] != "占用者" {
				t.Errorf("UsedBy = %v", v.UsedBy)
			}
			return
		}
	}
	t.Fatal("没找到 streamlink")
}

func TestCustomToolCannotUpdate(t *testing.T) {
	// 自定义内核我们不知道它打哪儿来，不能提供在线更新
	c := newTestCore(t)
	if err := c.AddTool(config.Tool{ID: "my-drm", Name: "自定义", Path: `C:\x\a.exe`, Role: "fetch"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range c.ToolViews() {
		if v.ID == "my-drm" && v.CanUpdate {
			t.Error("自定义内核不应显示可更新")
		}
	}
}

// ---------- 内核增删改 ----------

func TestAddAndDeleteTool(t *testing.T) {
	c := newTestCore(t)
	if err := c.AddTool(config.Tool{ID: "my-drm", Name: "自定义", Path: `C:\x\a.exe`, Role: "fetch"}); err != nil {
		t.Fatalf("AddTool 失败: %v", err)
	}
	if len(c.ToolViews()) != 4 {
		t.Errorf("内核数 = %d", len(c.ToolViews()))
	}
	if err := c.DeleteTool("my-drm"); err != nil {
		t.Fatalf("DeleteTool 失败: %v", err)
	}
	if len(c.ToolViews()) != 3 {
		t.Error("内核未删除")
	}
}

func TestDeleteBuiltinToolRejected(t *testing.T) {
	c := newTestCore(t)
	if err := c.DeleteTool("streamlink"); err == nil {
		t.Error("内置内核不应能删除")
	}
}

func TestDeleteToolInUseRejected(t *testing.T) {
	c := newTestCore(t)
	if err := c.AddTool(config.Tool{ID: "my-drm", Name: "自定义", Path: `C:\x\a.exe`, Role: "fetch"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddTask(config.Task{Name: "用它的任务", SourceURL: "https://x", ToolID: "my-drm",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}}); err != nil {
		t.Fatal(err)
	}
	err := c.DeleteTool("my-drm")
	if err == nil {
		t.Fatal("被任务引用的内核不应能删除")
	}
	if !strings.Contains(err.Error(), "用它的任务") {
		t.Errorf("报错应点名是哪个任务在用: %v", err)
	}
}

func TestToolMutationsPersist(t *testing.T) {
	c := newTestCore(t)
	if err := c.AddTool(config.Tool{ID: "my-drm", Name: "自定义", Path: `C:\x\a.exe`, Role: "fetch"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(c.dataDir + "/config.json")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tl := range loaded.Tools {
		if tl.ID == "my-drm" {
			found = true
		}
	}
	if !found {
		t.Error("新增内核未落盘")
	}
}

// ---------- 路径覆盖 ----------

func TestSetAndResetToolPath(t *testing.T) {
	c := newTestCore(t)
	if err := c.SetToolPath("streamlink", `C:\bin\streamlink.exe`); err != nil {
		t.Fatalf("SetToolPath 失败: %v", err)
	}
	var v ToolView
	for _, x := range c.ToolViews() {
		if x.ID == "streamlink" {
			v = x
		}
	}
	if !v.HasOverride || v.Path != `C:\bin\streamlink.exe` {
		t.Errorf("覆盖未生效: %+v", v)
	}

	if err := c.ResetToolPath("streamlink"); err != nil {
		t.Fatalf("ResetToolPath 失败: %v", err)
	}
	for _, x := range c.ToolViews() {
		if x.ID == "streamlink" && x.HasOverride {
			t.Error("恢复默认后不该还有覆盖")
		}
	}
}

func TestToolPathChangeRejectedWhileInUse(t *testing.T) {
	// 推流中把内核路径换掉，下一次重连就会用另一个二进制，行为莫名其妙
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}})
	if err != nil {
		t.Fatal(err)
	}
	c.states[got.ID] = core.StateRunning

	if err := c.SetToolPath("streamlink", `C:\bin\other.exe`); err == nil {
		t.Error("内核正被运行中的任务使用时不应允许改路径")
	}
}

// ---------- 探测 ----------

func TestProbeToolWritesVersion(t *testing.T) {
	// 用 go 自己当被探测对象：它一定存在，且会打印版本号
	c := newTestCore(t)
	if err := c.AddTool(config.Tool{ID: "go-probe", Name: "go", Path: "go", Role: "fetch"}); err != nil {
		t.Fatal(err)
	}
	info, err := c.ProbeTool(context.Background(), "go-probe")
	if err != nil {
		t.Skipf("PATH 中无 go，跳过: %v", err)
	}
	if !strings.HasPrefix(info.Version, "1.") {
		t.Errorf("Version = %q", info.Version)
	}
	for _, v := range c.ToolViews() {
		if v.ID == "go-probe" && v.Version == "" {
			t.Error("探测结果未写回内核列表")
		}
	}
}

func TestProbeMissingTool(t *testing.T) {
	c := newTestCore(t)
	if _, err := c.ProbeTool(context.Background(), "不存在"); err == nil {
		t.Error("探测不存在的内核应报错")
	}
}

// ---------- 微博 ----------

func TestWeiboViewWithoutCookie(t *testing.T) {
	c := newTestCore(t)
	v := c.WeiboView()
	if v.Status != "absent" {
		t.Errorf("Status = %q, 期望 absent", v.Status)
	}
	if v.StatusText == "" {
		t.Error("应给出中文状态说明")
	}
	if v.Usable {
		t.Error("没录入 cookie 时不应可用")
	}
}

func TestWeiboViewNeverLeaksCookie(t *testing.T) {
	// 这个结构会被整个序列化丢给 WebView
	c := newTestCore(t)
	v := c.WeiboView()
	blob := v.Status + v.StatusText + v.CheckedAt + v.Detail
	if strings.Contains(blob, "SUB=") {
		t.Errorf("微博视图里出现了 cookie 片段: %+v", v)
	}
}

func TestClearWeiboCookie(t *testing.T) {
	c := newTestCore(t)
	// 没录入时清除也不该报错：用户可能就是想确认一下
	if err := c.ClearWeiboCookie(); err != nil {
		t.Errorf("ClearWeiboCookie 失败: %v", err)
	}
}

// ---------- 更新 ----------

func TestUpgradeCustomToolRejected(t *testing.T) {
	c := newTestCore(t)
	if err := c.AddTool(config.Tool{ID: "my-drm", Name: "自定义", Path: `C:\x\a.exe`, Role: "fetch"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CheckToolUpdate(context.Background(), "my-drm"); err == nil {
		t.Error("自定义内核没有更新来源，应明确报错而不是去网上乱找")
	}
}

func TestUpgradeToolInUseRejected(t *testing.T) {
	c := newTestCore(t)
	got, err := c.AddTask(config.Task{Name: "t", SourceURL: "https://x", ToolID: "streamlink",
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a"}}})
	if err != nil {
		t.Fatal(err)
	}
	c.states[got.ID] = core.StateRunning

	if _, err := c.UpgradeTool(context.Background(), "streamlink"); err == nil {
		t.Error("内核正被使用时不应允许更新——Windows 上换不掉，还会掐断直播")
	}
}

func TestToolViewSlicesAreNeverNil(t *testing.T) {
	// nil 切片会被序列化成 JSON 的 null，前端一句 usedBy.length 就抛异常，
	// 整个页面渲染失败——只剩一片空白，还看不出是哪儿坏了
	c := newTestCore(t)
	for _, v := range c.ToolViews() {
		if v.UsedBy == nil {
			t.Errorf("%s 的 UsedBy 是 nil，会序列化成 null", v.ID)
		}
	}
}

func TestTaskViewSlicesAreNeverNil(t *testing.T) {
	c := newTestCore(t)
	if _, err := c.AddTask(config.Task{Name: "微博", SourceURL: "https://x", ToolID: "streamlink",
		WeiboLive: true}); err != nil {
		t.Fatal(err)
	}
	for _, v := range c.TaskViews() {
		if v.Targets == nil {
			t.Errorf("%s 的 Targets 是 nil", v.ID)
		}
	}
}

func TestTaskViewsIsNeverNil(t *testing.T) {
	// 没有任务时也要返回空数组而不是 null，否则前端 v-for 拿到 null
	c := newTestCore(t)
	if c.TaskViews() == nil {
		t.Error("TaskViews 返回了 nil")
	}
	if c.Events("不存在") == nil {
		t.Error("Events 返回了 nil")
	}
}
