package weibo

import (
	"context"
	"fmt"
	"sync"

	"github.com/imbalaomao/liverelay/internal/config"
)

// Resolver 把开了"微博直播"的任务翻译成可推流的目标。
//
// 它作为 core.Manager.Prepare 挂进去，因此手动开播、无人值守自动开播、
// 断流重连都会经过这里，每次都拿到当时有效的推流地址。
type Resolver struct {
	svc *Service

	mu sync.Mutex
	// watch 记录每个任务最近一次拿到的 HLS 观看链接，供 UI 展示。
	watch map[string]string
	// keys 记录用过的推流码，供日志脱敏。
	keys map[string]struct{}
}

func NewResolver(svc *Service) *Resolver {
	return &Resolver{
		svc:   svc,
		watch: map[string]string{},
		keys:  map[string]struct{}{},
	}
}

// Prepare 实现 core.Prepare。
func (r *Resolver) Prepare(ctx context.Context, t config.Task) (config.Task, error) {
	if !t.WeiboLive {
		return t, nil
	}

	info, err := r.svc.StreamInfo(ctx)
	if err != nil {
		// 包一层说明，让用户在任务卡片上就知道该去settings里做什么
		return config.Task{}, fmt.Errorf("无法获取微博推流地址：%w", err)
	}

	// 复制一份再改：Manager 保存的是原始任务，就地改写会让下一轮重连
	// 带上一份重复的微博目标，同一条流被推两次
	out := t
	out.Targets = make([]config.Target, len(t.Targets), len(t.Targets)+1)
	copy(out.Targets, t.Targets)
	out.Targets = append(out.Targets, config.Target{
		Proto: "rtmp",
		URL:   info.PushURL,
		Key:   info.PushKey,
	})

	r.mu.Lock()
	r.watch[t.ID] = info.WatchHLS
	if info.PushKey != "" {
		r.keys[info.PushKey] = struct{}{}
	}
	r.mu.Unlock()
	return out, nil
}

// WatchURL 返回某个任务最近一次拿到的 HLS 观看链接，没有则为空串。
func (r *Resolver) WatchURL(taskID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.watch[taskID]
}

// Secrets 返回需要在日志里脱敏的串。
//
// 只收推流码：它一旦进了日志，等于把开播权限公开出去。
// cookie 不在这里——它只出现在 HTTP 请求头，从不流向子进程的输出，
// 把它放进一份会被四处传递、比对的名单反而是多开一个口子。
func (r *Resolver) Secrets() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.keys))
	for k := range r.keys {
		out = append(out, k)
	}
	return out
}
