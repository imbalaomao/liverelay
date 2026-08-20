package weibo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CheckInterval 是 cookie 有效性的复检周期。
// 三天是用户定的：微博 cookie 通常能活几周到几个月，查得太勤没有意义，
// 查得太稀又会让人在开播那一刻才发现登录失效。
const CheckInterval = 72 * time.Hour

// StateFileName 是状态文件名。它是明文的——里面只有状态与时间，没有凭据。
const StateFileName = "weibo_state.json"

// defaultTick 是后台循环的节拍。真正的判断按 CheckInterval 走，
// 这里只决定"多久看一眼是不是该检测了"。
const defaultTick = time.Hour

// Status 是微博 cookie 的当前状态。
type Status string

const (
	// StatusAbsent 还没录入。
	StatusAbsent Status = "absent"
	// StatusValid 最近一次检测确认可用。
	StatusValid Status = "valid"
	// StatusExpired 微博明确拒绝了这份 cookie，必须重新录入。
	StatusExpired Status = "expired"
	// StatusUnknown 没能得出结论（通常是连不上微博）。
	// 与 StatusExpired 分开是关键：断网不能变成"请重新登录"。
	StatusUnknown Status = "unknown"
)

func (s Status) Display() string {
	switch s {
	case StatusValid:
		return "有效"
	case StatusExpired:
		return "已失效"
	case StatusUnknown:
		return "无法判断"
	default:
		return "未录入"
	}
}

// State 是对外暴露的状态快照。
type State struct {
	Status    Status    `json:"status"`
	CheckedAt time.Time `json:"checkedAt"`
	Detail    string    `json:"detail"`
}

// Usable 报告现在能不能拿去生成直播链接。
// Unknown 也放行——那只是我们没验成功，不代表 cookie 真的坏了。
func (s State) Usable() bool {
	return s.Status == StatusValid || s.Status == StatusUnknown
}

// Service 管理微博 cookie 的存取、周期复检与推流地址获取。
type Service struct {
	dir    string
	store  *Store
	client *Client

	// fetch 与 now 可注入，测试用。
	fetch func(ctx context.Context, cookie string) (StreamInfo, error)
	now   func() time.Time
	tick  time.Duration

	mu    sync.Mutex
	state State
	// loaded 标记是否已从磁盘读过状态。
	loaded bool
}

func NewService(dataDir string) *Service {
	c := &Client{}
	s := &Service{
		dir:    dataDir,
		store:  NewStore(dataDir),
		client: c,
		now:    time.Now,
		tick:   defaultTick,
	}
	s.fetch = c.Fetch
	return s
}

func (s *Service) statePath() string { return filepath.Join(s.dir, StateFileName) }

// UseHTTPClient 换用调用方构造的 HTTP 客户端，通常是为了带上用户配置的代理。
// 不接代理的话，用户会遇到"别处都能用、偏偏微博这块连不上"。
// 传 nil 是空操作——把已有的客户端清掉只会让后续请求崩在空指针上。
func (s *Service) UseHTTPClient(c *http.Client) {
	if c == nil {
		return
	}
	s.client.HTTP = c
}

// State 返回当前状态快照，不发起网络请求。
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	return s.state
}

// ensureLoadedLocked 首次访问时从磁盘恢复状态。
// 状态跨重启保留，否则每次开程序都会重新检测一遍，三天周期形同虚设。
func (s *Service) ensureLoadedLocked() {
	if s.loaded {
		return
	}
	s.loaded = true

	if !s.store.Exists() {
		s.state = State{Status: StatusAbsent}
		return
	}
	body, err := os.ReadFile(s.statePath())
	if err != nil {
		// 有 cookie 但没有状态记录：当作没检测过，交给下一次 EnsureChecked
		s.state = State{Status: StatusUnknown, Detail: "尚未检测"}
		return
	}
	var st State
	if err := json.Unmarshal(body, &st); err != nil || st.Status == "" {
		s.state = State{Status: StatusUnknown, Detail: "状态记录已损坏，将重新检测"}
		return
	}
	s.state = st
}

func (s *Service) setStateLocked(st State) {
	s.state = st
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(s.dir, 0o755)
	// 状态丢了最多多检测一次，不值得为它中断主流程
	_ = os.WriteFile(s.statePath(), body, 0o600)
}

// SaveCookie 先验证再保存。
//
// 验证失败就不保存：存下一份明知无效的 cookie，只会让用户以为配好了，
// 到开播那一刻才发现推不出去。
func (s *Service) SaveCookie(ctx context.Context, cookie string) (State, error) {
	info, err := s.fetch(ctx, cookie)
	_ = info

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()

	switch {
	case err == nil:
		if serr := s.store.Save(cookie); serr != nil {
			return s.state, serr
		}
		st := State{Status: StatusValid, CheckedAt: s.now(), Detail: "已验证可用"}
		s.setStateLocked(st)
		return st, nil

	case errors.Is(err, ErrExpired):
		st := State{Status: StatusExpired, CheckedAt: s.now(),
			Detail: "微博拒绝了这份 cookie，请重新登录网页版后复制"}
		s.setStateLocked(st)
		return st, err

	case errors.Is(err, ErrNoCookie):
		return s.state, err

	default:
		// 连不上微博时无法判断新 cookie 是否有效，不能因此把已有的顶掉
		return s.state, fmt.Errorf("无法验证 cookie（未改动已保存的凭据）: %w", err)
	}
}

// ClearCookie 删除已保存的 cookie 与状态。
func (s *Service) ClearCookie() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.Clear(); err != nil {
		return err
	}
	s.loaded = true
	s.setStateLocked(State{Status: StatusAbsent})
	return nil
}

// Check 立即检测一次并更新状态。
func (s *Service) Check(ctx context.Context) State {
	cookie, err := s.store.Load()
	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.loaded = true
		s.setStateLocked(State{Status: StatusAbsent, Detail: err.Error()})
		return s.state
	}

	_, ferr := s.fetch(ctx, cookie)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	s.setStateLocked(s.judgeLocked(ferr))
	return s.state
}

// judgeLocked 把一次检测的结果翻译成状态。
func (s *Service) judgeLocked(err error) State {
	now := s.now()
	switch {
	case err == nil:
		return State{Status: StatusValid, CheckedAt: now, Detail: "已验证可用"}

	case errors.Is(err, ErrExpired):
		return State{Status: StatusExpired, CheckedAt: now,
			Detail: "微博拒绝了这份 cookie，请重新登录网页版后复制"}

	case errors.Is(err, ErrNoCookie):
		return State{Status: StatusAbsent, CheckedAt: now}

	default:
		// 连不上不代表 cookie 坏了。已经验证过有效的就维持有效，
		// 只在说明里如实写上这次没检测成功
		detail := "检测失败（不改变已有判定）: " + err.Error()
		if s.state.Status == StatusValid {
			return State{Status: StatusValid, CheckedAt: s.state.CheckedAt, Detail: detail}
		}
		return State{Status: StatusUnknown, CheckedAt: s.state.CheckedAt, Detail: detail}
	}
}

// EnsureChecked 距上次成功检测超过 CheckInterval 时才真正检测。
func (s *Service) EnsureChecked(ctx context.Context) State {
	s.mu.Lock()
	s.ensureLoadedLocked()
	st := s.state
	now := s.now()
	s.mu.Unlock()

	if st.Status == StatusAbsent && !s.store.Exists() {
		return st
	}
	if !st.CheckedAt.IsZero() && now.Sub(st.CheckedAt) < CheckInterval {
		return st
	}
	return s.Check(ctx)
}

// Run 定期复检，直到 ctx 取消。
func (s *Service) Run(ctx context.Context) {
	s.EnsureChecked(ctx)
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.EnsureChecked(ctx)
		}
	}
}

// StreamInfo 取当次开播用的推流地址与观看地址。
//
// 已知过期时直接拒绝、不发请求：用户要的是"过期就别再生成链接了"，
// 而不是每次点开播都对微博白打一次。
func (s *Service) StreamInfo(ctx context.Context) (StreamInfo, error) {
	s.mu.Lock()
	s.ensureLoadedLocked()
	st := s.state
	s.mu.Unlock()

	if st.Status == StatusExpired {
		return StreamInfo{}, ErrExpired
	}
	cookie, err := s.store.Load()
	if err != nil {
		return StreamInfo{}, err
	}

	info, ferr := s.fetch(ctx, cookie)
	if ferr != nil {
		// 开播时才发现过期，就地更新状态，不用等下一个三天
		s.mu.Lock()
		s.setStateLocked(s.judgeLocked(ferr))
		s.mu.Unlock()
		return StreamInfo{}, ferr
	}

	s.mu.Lock()
	s.setStateLocked(State{Status: StatusValid, CheckedAt: s.now(), Detail: "已验证可用"})
	s.mu.Unlock()
	return info, nil
}
