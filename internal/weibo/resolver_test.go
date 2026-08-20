package weibo

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/imbalaomao/liverelay/internal/config"
)

func testResolver(t *testing.T) (*Resolver, *fakeFetch) {
	t.Helper()
	s, ff, _ := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	return NewResolver(s), ff
}

func weiboTask() config.Task {
	return config.Task{
		ID: "t1", Name: "微博任务", SourceURL: "https://example.com/live",
		ToolID: "streamlink", WeiboLive: true,
	}
}

// ---------- 放行与改写 ----------

func TestPrepareLeavesNonWeiboTaskAlone(t *testing.T) {
	r, ff := testResolver(t)
	task := config.Task{ID: "t2", Name: "普通任务", WeiboLive: false,
		Targets: []config.Target{{Proto: "rtmp", URL: "rtmp://a/b", Key: "k"}}}
	before := ff.count()

	got, err := r.Prepare(context.Background(), task)
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	if len(got.Targets) != 1 {
		t.Errorf("非微博任务的目标被改动了: %+v", got.Targets)
	}
	if ff.count() != before {
		t.Error("非微博任务不该发起微博请求")
	}
}

func TestPrepareAppendsWeiboTarget(t *testing.T) {
	r, _ := testResolver(t)
	task := weiboTask()
	task.Targets = []config.Target{{Proto: "rtmp", URL: "rtmp://b站/live", Key: "bkey"}}

	got, err := r.Prepare(context.Background(), task)
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	// 追加而不是替换：一路源同时推微博和别处是常见用法
	if len(got.Targets) != 2 {
		t.Fatalf("目标数 = %d, 期望 2: %+v", len(got.Targets), got.Targets)
	}
	if got.Targets[0].URL != "rtmp://b站/live" {
		t.Errorf("原有目标被动过了: %+v", got.Targets[0])
	}
	wb := got.Targets[1]
	if wb.URL != okInfo.PushURL || wb.Key != okInfo.PushKey {
		t.Errorf("微博目标 = %+v", wb)
	}
	if wb.Proto != "rtmp" {
		t.Errorf("Proto = %q", wb.Proto)
	}
}

func TestPrepareRecordsWatchURL(t *testing.T) {
	// 观看链接是这个功能对用户最直接的产出，必须能拿得到
	r, _ := testResolver(t)
	if _, err := r.Prepare(context.Background(), weiboTask()); err != nil {
		t.Fatal(err)
	}
	if got := r.WatchURL("t1"); got != okInfo.WatchHLS {
		t.Errorf("WatchURL = %q, 期望 %q", got, okInfo.WatchHLS)
	}
}

func TestWatchURLUnknownTask(t *testing.T) {
	r, _ := testResolver(t)
	if got := r.WatchURL("没这个任务"); got != "" {
		t.Errorf("WatchURL = %q, 期望空", got)
	}
}

func TestPrepareRefreshesWatchURL(t *testing.T) {
	// 重连时地址可能已经换了，记录的观看链接要跟着更新
	r, ff := testResolver(t)
	if _, err := r.Prepare(context.Background(), weiboTask()); err != nil {
		t.Fatal(err)
	}
	ff.set(StreamInfo{PushURL: "rtmp://new/", PushKey: "k2",
		WatchHLS: "https://new/watch.m3u8"}, nil)
	if _, err := r.Prepare(context.Background(), weiboTask()); err != nil {
		t.Fatal(err)
	}
	if got := r.WatchURL("t1"); got != "https://new/watch.m3u8" {
		t.Errorf("WatchURL 未更新: %q", got)
	}
}

// ---------- 拒绝路径 ----------

func TestPrepareRefusesWhenExpired(t *testing.T) {
	r, ff := testResolver(t)
	ff.set(StreamInfo{}, ErrExpired)

	_, err := r.Prepare(context.Background(), weiboTask())
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("过期时应拒绝启动，实际 %v", err)
	}
	// 报错要说清楚该干什么，不能只甩一句"失败"
	if !strings.Contains(err.Error(), "微博") {
		t.Errorf("错误信息应说明是微博 cookie 的问题: %v", err)
	}
}

func TestPrepareRefusesWithoutCookie(t *testing.T) {
	s, _, _ := testSvc(t)
	r := NewResolver(s)

	_, err := r.Prepare(context.Background(), weiboTask())
	if !errors.Is(err, ErrNoCookie) {
		t.Errorf("未录入 cookie 时应拒绝，实际 %v", err)
	}
}

func TestPrepareDoesNotMutateInput(t *testing.T) {
	// Manager 保存的是原始任务，被就地改写会让下一轮重连带上重复的目标
	r, _ := testResolver(t)
	task := weiboTask()
	task.Targets = []config.Target{{Proto: "rtmp", URL: "rtmp://b站/live", Key: "bkey"}}

	if _, err := r.Prepare(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if len(task.Targets) != 1 {
		t.Errorf("原任务被就地改写了: %+v", task.Targets)
	}
}

func TestPrepareTwiceDoesNotAccumulateTargets(t *testing.T) {
	r, _ := testResolver(t)
	task := weiboTask()

	first, err := r.Prepare(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Prepare(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Targets) != 1 || len(second.Targets) != 1 {
		t.Errorf("重复解析累积出了多个目标: %d / %d", len(first.Targets), len(second.Targets))
	}
}

// ---------- 脱敏 ----------

func TestSecretsIncludesPushKey(t *testing.T) {
	// 推流码进了日志等于把开播权限公开出去
	r, _ := testResolver(t)
	if _, err := r.Prepare(context.Background(), weiboTask()); err != nil {
		t.Fatal(err)
	}
	secrets := r.Secrets()
	found := false
	for _, s := range secrets {
		if s == okInfo.PushKey {
			found = true
		}
	}
	if !found {
		t.Errorf("推流码未进脱敏名单: %v", secrets)
	}
}

func TestSecretsNeverContainsCookie(t *testing.T) {
	// cookie 只在 HTTP 请求头里出现，绝不能流进日志脱敏名单之外的任何地方；
	// 但它也不该出现在脱敏名单里——名单本身是要被比对、被传递的
	r, _ := testResolver(t)
	if _, err := r.Prepare(context.Background(), weiboTask()); err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Secrets() {
		if strings.Contains(s, "superSECRETvalue") {
			t.Errorf("cookie 出现在了脱敏名单里: %q", s)
		}
	}
}
