package weibo

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeFetch 造一个可控的查询结果。
type fakeFetch struct {
	mu    sync.Mutex
	calls int
	info  StreamInfo
	err   error
}

func (f *fakeFetch) fetch(context.Context, string) (StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.info, f.err
}

func (f *fakeFetch) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeFetch) set(info StreamInfo, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.info, f.err = info, err
}

var okInfo = StreamInfo{
	PushURL:  "rtmp://push.alive.sinaimg.cn/alive/",
	PushKey:  "abc123",
	WatchHLS: "https://f.us.sinaimg.cn/alive/abc123.m3u8",
}

func testSvc(t *testing.T) (*Service, *fakeFetch, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	store := NewStore(dir)
	p, u := fakeCrypto()
	store.protect, store.unprotect = p, u

	ff := &fakeFetch{info: okInfo}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	s := NewService(dir)
	s.store = store
	s.fetch = ff.fetch
	s.now = func() time.Time { return now }
	return s, ff, &now
}

// ---------- 录入 ----------

func TestStateWithoutCookie(t *testing.T) {
	s, ff, _ := testSvc(t)
	st := s.State()
	if st.Status != StatusAbsent {
		t.Errorf("Status = %q, 期望 absent", st.Status)
	}
	if ff.count() != 0 {
		t.Error("没有 cookie 时不该发起网络请求")
	}
}

func TestSaveCookieVerifiesBeforeStoring(t *testing.T) {
	s, ff, _ := testSvc(t)
	st, err := s.SaveCookie(context.Background(), sampleCookie)
	if err != nil {
		t.Fatalf("SaveCookie 失败: %v", err)
	}
	if st.Status != StatusValid {
		t.Errorf("Status = %q, 期望 valid", st.Status)
	}
	if ff.count() != 1 {
		t.Errorf("录入时应先验证一次，实际请求 %d 次", ff.count())
	}
	if !s.store.Exists() {
		t.Error("验证通过后应已保存")
	}
}

func TestSaveCookieRejectsExpiredOne(t *testing.T) {
	// 明知无效还存下来，只会让用户以为配好了、到开播时才发现推不出去
	s, ff, _ := testSvc(t)
	ff.set(StreamInfo{}, ErrExpired)

	st, err := s.SaveCookie(context.Background(), sampleCookie)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("应拒绝无效 cookie，实际 %v", err)
	}
	if st.Status != StatusExpired {
		t.Errorf("Status = %q", st.Status)
	}
	if s.store.Exists() {
		t.Error("无效的 cookie 不该落盘")
	}
}

func TestSaveCookieKeepsOldOnNetworkFailure(t *testing.T) {
	// 断网时无法判断新 cookie 是否有效，不能因此把已有的好 cookie 顶掉
	s, ff, _ := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), "good=1"); err != nil {
		t.Fatal(err)
	}
	ff.set(StreamInfo{}, errors.New("connection refused"))

	if _, err := s.SaveCookie(context.Background(), "new=2"); err == nil {
		t.Fatal("网络故障时 SaveCookie 应报错")
	}
	got, err := s.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != "good=1" {
		t.Errorf("原 cookie 被覆盖了: %q", got)
	}
}

func TestClearCookie(t *testing.T) {
	s, _, _ := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearCookie(); err != nil {
		t.Fatalf("ClearCookie 失败: %v", err)
	}
	if st := s.State(); st.Status != StatusAbsent {
		t.Errorf("Status = %q, 期望 absent", st.Status)
	}
	if s.store.Exists() {
		t.Error("cookie 文件应已删除")
	}
}

// ---------- 三天周期 ----------

func TestEnsureCheckedSkipsWithinInterval(t *testing.T) {
	s, ff, now := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	before := ff.count()

	*now = now.Add(CheckInterval - time.Hour)
	s.EnsureChecked(context.Background())
	if ff.count() != before {
		t.Errorf("未到 %v 检测周期不应发起请求", CheckInterval)
	}
}

func TestEnsureCheckedRunsAfterInterval(t *testing.T) {
	s, ff, now := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	before := ff.count()

	*now = now.Add(CheckInterval + time.Minute)
	st := s.EnsureChecked(context.Background())
	if ff.count() != before+1 {
		t.Errorf("超过检测周期应发起一次请求，实际 %d → %d", before, ff.count())
	}
	if st.Status != StatusValid {
		t.Errorf("Status = %q", st.Status)
	}
}

func TestExpiryDetectedByPeriodicCheck(t *testing.T) {
	s, ff, now := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	ff.set(StreamInfo{}, ErrExpired)
	*now = now.Add(CheckInterval + time.Minute)

	if st := s.EnsureChecked(context.Background()); st.Status != StatusExpired {
		t.Errorf("周期检测应发现过期，实际 %q", st.Status)
	}
}

func TestNetworkFailureDoesNotMarkExpired(t *testing.T) {
	// 用户断个网就被判过期、被要求重新登录一遍，是最招人烦的伪故障
	s, ff, now := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	ff.set(StreamInfo{}, errors.New("dial tcp: connection refused"))
	*now = now.Add(CheckInterval + time.Minute)

	st := s.EnsureChecked(context.Background())
	if st.Status == StatusExpired {
		t.Error("网络故障不应判定为 cookie 过期")
	}
	if st.Status != StatusValid {
		t.Errorf("应维持上次的有效状态，实际 %q", st.Status)
	}
	if !strings.Contains(st.Detail, "检测失败") {
		t.Errorf("应说明这次没检测成功，实际 %q", st.Detail)
	}
}

func TestNetworkFailureBeforeAnyValidCheck(t *testing.T) {
	// 从没成功过又连不上时，只能如实说"判断不了"
	s, ff, _ := testSvc(t)
	if err := s.store.Save(sampleCookie); err != nil {
		t.Fatal(err)
	}
	ff.set(StreamInfo{}, errors.New("connection refused"))

	if st := s.Check(context.Background()); st.Status != StatusUnknown {
		t.Errorf("Status = %q, 期望 unknown", st.Status)
	}
}

// ---------- 状态持久化 ----------

func TestStateSurvivesRestart(t *testing.T) {
	// 每次开程序都重新检测一遍，等于把三天周期变成了每次启动
	s, ff, _ := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	calls := ff.count()

	s2 := NewService(s.dir)
	s2.store = s.store
	s2.fetch = ff.fetch
	s2.now = s.now

	st := s2.State()
	if st.Status != StatusValid {
		t.Errorf("重启后 Status = %q, 期望 valid", st.Status)
	}
	s2.EnsureChecked(context.Background())
	if ff.count() != calls {
		t.Error("重启后不该立刻重新检测——三天周期应当跨重启生效")
	}
}

func TestStateFileIsNotSecret(t *testing.T) {
	// 状态文件是明文的，绝不能把 cookie 写进去
	s, _, _ := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "superSECRETvalue") {
		t.Errorf("状态文件里出现了 cookie:\n%s", raw)
	}
}

func TestCorruptedStateFileFallsBackToRecheck(t *testing.T) {
	s, ff, _ := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.statePath(), []byte("坏掉的内容"), 0o600); err != nil {
		t.Fatal(err)
	}

	s2 := NewService(s.dir)
	s2.store, s2.fetch, s2.now = s.store, ff.fetch, s.now
	before := ff.count()
	s2.EnsureChecked(context.Background())
	if ff.count() != before+1 {
		t.Error("状态文件损坏时应重新检测一次，而不是当成从没检测过就放着不管")
	}
}

// ---------- 取推流地址 ----------

func TestStreamInfoRefusedWhenExpired(t *testing.T) {
	// "过期则不能再生成直播链接"——而且要在发请求之前就拦住
	s, ff, _ := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	ff.set(StreamInfo{}, ErrExpired)
	s.Check(context.Background())
	before := ff.count()

	if _, err := s.StreamInfo(context.Background()); !errors.Is(err, ErrExpired) {
		t.Errorf("过期后应拒绝生成直播链接，实际 %v", err)
	}
	if ff.count() != before {
		t.Error("已知过期时不该再发请求")
	}
}

func TestStreamInfoRefusedWithoutCookie(t *testing.T) {
	s, _, _ := testSvc(t)
	if _, err := s.StreamInfo(context.Background()); !errors.Is(err, ErrNoCookie) {
		t.Error("未录入 cookie 时应拒绝")
	}
}

func TestStreamInfoSucceeds(t *testing.T) {
	s, _, _ := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	info, err := s.StreamInfo(context.Background())
	if err != nil {
		t.Fatalf("StreamInfo 失败: %v", err)
	}
	if info.PushURL != okInfo.PushURL || info.WatchHLS != okInfo.WatchHLS {
		t.Errorf("info = %+v", info)
	}
}

func TestStreamInfoRefreshesStatusOnExpiry(t *testing.T) {
	// 开播时才发现过期，状态要就地更新，不用等下一个三天
	s, ff, _ := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	ff.set(StreamInfo{}, ErrExpired)

	if _, err := s.StreamInfo(context.Background()); !errors.Is(err, ErrExpired) {
		t.Fatalf("实际 %v", err)
	}
	if st := s.State(); st.Status != StatusExpired {
		t.Errorf("取地址时发现过期应就地更新状态，实际 %q", st.Status)
	}
}

func TestRecoveryAfterReentry(t *testing.T) {
	s, ff, _ := testSvc(t)
	if _, err := s.SaveCookie(context.Background(), sampleCookie); err != nil {
		t.Fatal(err)
	}
	ff.set(StreamInfo{}, ErrExpired)
	s.Check(context.Background())

	// 用户重新登录后录入新 cookie，应当恢复可用
	ff.set(okInfo, nil)
	st, err := s.SaveCookie(context.Background(), "fresh=1")
	if err != nil {
		t.Fatalf("重新录入失败: %v", err)
	}
	if st.Status != StatusValid {
		t.Errorf("Status = %q, 期望恢复为 valid", st.Status)
	}
	if _, err := s.StreamInfo(context.Background()); err != nil {
		t.Errorf("恢复后应能取到地址: %v", err)
	}
}

// ---------- 生命周期 ----------

func TestRunStopsOnCancel(t *testing.T) {
	s, _, _ := testSvc(t)
	s.tick = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run 未在 context 取消后退出")
	}
}

func TestUseHTTPClientAppliesProxyAwareClient(t *testing.T) {
	// 微博在国内也常有人挂代理访问；不接上配置里的代理设置，
	// 用户会遇到"别处都能用、就微博这块连不上"
	s := NewService(t.TempDir())
	custom := &http.Client{Timeout: 3 * time.Second}
	s.UseHTTPClient(custom)

	if s.client.HTTP != custom {
		t.Error("UseHTTPClient 未生效")
	}
}

func TestUseHTTPClientIgnoresNil(t *testing.T) {
	s := NewService(t.TempDir())
	before := s.client.HTTP
	s.UseHTTPClient(nil)
	if s.client.HTTP != before {
		t.Error("传 nil 不应把已有的客户端清掉")
	}
}
