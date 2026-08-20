package appcore

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/paths"
	"github.com/imbalaomao/liverelay/internal/tools"
	"github.com/imbalaomao/liverelay/internal/updater"
	"github.com/imbalaomao/liverelay/internal/weibo"
)

// ToolView 是内核卡片需要的一切。
type ToolView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Builtin  bool   `json:"builtin"`
	Role     string `json:"role"`
	RoleText string `json:"roleText"`
	// Path 是解析后的实际执行路径，用户得知道程序到底在用哪个文件。
	Path        string `json:"path"`
	HasOverride bool   `json:"hasOverride"`
	Version     string `json:"version"`
	CapSummary  string `json:"capSummary"`
	// CanUpdate 表示有内置的在线更新来源。自定义内核我们不知道它打哪儿来。
	CanUpdate bool `json:"canUpdate"`
	// UsedBy 列出引用它的任务名，删除受阻时要说清楚是谁在用。
	UsedBy []string `json:"usedBy"`
	InUse  bool     `json:"inUse"`
}

// nonNil 把 nil 切片换成空切片，避免序列化成 JSON 的 null。
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func roleText(role string) string {
	switch role {
	case "fetch":
		return "抓流"
	case "record":
		return "录制"
	case "both":
		return "抓流/录制"
	default:
		return role
	}
}

// ToolViews 返回内核列表。
func (c *Core) ToolViews() []ToolView {
	cfg := c.snapshot()
	out := make([]ToolView, 0, len(cfg.Tools))
	for _, t := range cfg.Tools {
		_, canUpdate := updater.SourceFor(t.ID)
		out = append(out, ToolView{
			ID: t.ID, Name: t.Name, Builtin: t.Builtin,
			Role: t.Role, RoleText: roleText(t.Role),
			Path:        tools.Resolved(c.dataDir, t),
			HasOverride: t.PathOverride != "",
			Version:     t.Version, CapSummary: t.CapSummary,
			CanUpdate: canUpdate,
			// 必须是空数组而不是 nil：nil 会序列化成 JSON 的 null，
			// 前端一句 usedBy.length 就抛异常，整页渲染失败只剩一片空白
			UsedBy: nonNil(tools.UsedBy(cfg, t.ID)),
			InUse:  c.toolInUse(t.ID),
		})
	}
	return out
}

// mutateTools 在一次加锁里完成"复制配置 → 改内核列表 → 换上去"，再落盘。
// 复制而非就地改：Manager 与 monitor 可能正拿着旧指针在读。
func (c *Core) mutateTools(fn func(*config.Config) error) error {
	c.mu.Lock()
	next := *c.cfg
	next.Tools = append([]config.Tool(nil), c.cfg.Tools...)
	if err := fn(&next); err != nil {
		c.mu.Unlock()
		return err
	}
	c.cfg = &next
	c.mu.Unlock()
	return c.commit()
}

// AddTool 新增自定义内核。
func (c *Core) AddTool(t config.Tool) error {
	return c.mutateTools(func(cfg *config.Config) error { return tools.Add(cfg, t) })
}

// EditTool 修改自定义内核。
func (c *Core) EditTool(t config.Tool) error {
	if c.toolInUse(t.ID) {
		return fmt.Errorf("内核正被运行中的任务使用，请先停止相关任务")
	}
	return c.mutateTools(func(cfg *config.Config) error { return tools.Update(cfg, t) })
}

// DeleteTool 删除自定义内核。
func (c *Core) DeleteTool(id string) error {
	return c.mutateTools(func(cfg *config.Config) error { return tools.Delete(cfg, id) })
}

// SetToolPath 指定内核使用本地的某个可执行文件。
func (c *Core) SetToolPath(id, path string) error {
	// 推流中把路径换掉，下一次重连就会用另一个二进制，行为莫名其妙
	if c.toolInUse(id) {
		return fmt.Errorf("内核正被运行中的任务使用，请先停止相关任务")
	}
	return c.mutateTools(func(cfg *config.Config) error { return tools.SetOverride(cfg, id, path) })
}

// ResetToolPath 恢复内核的默认路径。
func (c *Core) ResetToolPath(id string) error {
	if c.toolInUse(id) {
		return fmt.Errorf("内核正被运行中的任务使用，请先停止相关任务")
	}
	return c.mutateTools(func(cfg *config.Config) error { return tools.ResetOverride(cfg, id) })
}

// ProbeTool 探测内核的版本与能力，结果写回配置。
func (c *Core) ProbeTool(ctx context.Context, id string) (tools.Info, error) {
	cfg := c.snapshot()
	tool, ok := tools.Find(cfg, id)
	if !ok {
		return tools.Info{}, fmt.Errorf("内核 %s 不存在", id)
	}
	info, err := c.prober.Probe(ctx, tools.Resolved(c.dataDir, tool))
	if err != nil {
		return tools.Info{}, err
	}
	if err := c.mutateTools(func(cfg *config.Config) error {
		tools.SetProbe(cfg, id, info)
		return nil
	}); err != nil {
		return info, err
	}
	return info, nil
}

// ReleaseView 是一次更新检查的结果。
type ReleaseView struct {
	ToolID    string `json:"toolId"`
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	// Rolling 表示对端用的是滚动标签，比不出新旧，只能由用户自己决定要不要更。
	Rolling bool   `json:"rolling"`
	SizeMB  string `json:"sizeMb"`
	Note    string `json:"note"`
}

// CheckToolUpdate 查询某个内核的最新版本。
func (c *Core) CheckToolUpdate(ctx context.Context, id string) (ReleaseView, error) {
	src, ok := updater.SourceFor(id)
	if !ok {
		return ReleaseView{}, fmt.Errorf("内核 %s 没有内置的更新来源，请手动替换可执行文件", id)
	}
	tool, _ := tools.Find(c.snapshot(), id)

	rel, err := c.up.Check(ctx, src)
	if err != nil {
		return ReleaseView{}, err
	}
	v := ReleaseView{
		ToolID: id, Current: tool.Version, Latest: rel.Version,
		Available: rel.UpdateAvailable(tool.Version),
		Rolling:   rel.Rolling,
		SizeMB:    fmt.Sprintf("%.1f", float64(rel.Size)/(1<<20)),
	}
	switch {
	case rel.Rolling:
		v.Note = "对方用的是滚动构建，比不出新旧，可手动更新"
	case v.Available:
		v.Note = "有新版本"
	default:
		v.Note = "已是最新"
	}
	return v, nil
}

// UpgradeTool 下载并换入新版本，成功后把新路径写回配置并重新探测版本。
func (c *Core) UpgradeTool(ctx context.Context, id string) (ReleaseView, error) {
	src, ok := updater.SourceFor(id)
	if !ok {
		return ReleaseView{}, fmt.Errorf("内核 %s 没有内置的更新来源，请手动替换可执行文件", id)
	}
	// Windows 上运行中的 exe 换不掉；就算换得掉，半路抽走内核会掐断直播
	if c.toolInUse(id) {
		return ReleaseView{}, fmt.Errorf("内核正被运行中的任务使用，请先停止相关任务再更新")
	}

	res, err := c.up.Update(ctx, src, paths.Tools(c.dataDir))
	if err != nil {
		return ReleaseView{}, err
	}

	// 整包落地的内核（streamlink）换完位置会变，不写回配置就会继续用着旧目录
	rel, rerr := relToDataDir(c.dataDir, res.BinaryPath)
	if rerr == nil {
		if err := c.mutateTools(func(cfg *config.Config) error {
			return setToolPathAfterUpgrade(cfg, id, rel)
		}); err != nil {
			c.logf("更新成功但写回路径失败: %v", err)
		}
	}
	if _, err := c.ProbeTool(ctx, id); err != nil {
		c.logf("更新成功但重新探测版本失败: %v", err)
	}

	tool, _ := tools.Find(c.snapshot(), id)
	return ReleaseView{
		ToolID: id, Current: tool.Version, Latest: res.Release.Version,
		Rolling: res.Release.Rolling, Note: "已更新",
	}, nil
}

// setToolPathAfterUpgrade 更新内置内核的默认路径。
// 走的是 Path 而不是 PathOverride——这是我们自己装进去的，不是用户指定的本地文件。
func setToolPathAfterUpgrade(cfg *config.Config, id, relPath string) error {
	for i := range cfg.Tools {
		if cfg.Tools[i].ID == id {
			cfg.Tools[i].Path = relPath
			return nil
		}
	}
	return fmt.Errorf("内核 %s 不存在", id)
}

// relToDataDir 把绝对路径折回相对数据根的形式，便携目录整体搬家后仍然有效。
// 配置里存绝对路径的话，用户把便携目录换个位置就全失效了。
func relToDataDir(dataDir, abs string) (string, error) {
	rel, err := filepath.Rel(dataDir, abs)
	if err != nil {
		return "", err
	}
	// 落回配置统一用正斜杠，与内置默认值保持一致
	return filepath.ToSlash(rel), nil
}

// WeiboView 是设置页展示微博状态需要的一切。
// 有意只放状态与时间：cookie 绝不能出现在会被序列化给 WebView 的结构里。
type WeiboView struct {
	Status     string `json:"status"`
	StatusText string `json:"statusText"`
	CheckedAt  string `json:"checkedAt"`
	Detail     string `json:"detail"`
	Usable     bool   `json:"usable"`
}

func toWeiboView(st weibo.State) WeiboView {
	v := WeiboView{
		Status:     string(st.Status),
		StatusText: st.Status.Display(),
		Detail:     st.Detail,
		Usable:     st.Usable(),
	}
	if !st.CheckedAt.IsZero() {
		v.CheckedAt = st.CheckedAt.Local().Format("2006-01-02 15:04")
	}
	return v
}

// WeiboView 返回当前微博 cookie 状态，不发起网络请求。
func (c *Core) WeiboView() WeiboView { return toWeiboView(c.weibo.State()) }

// SaveWeiboCookie 录入 cookie。会先验证再保存。
func (c *Core) SaveWeiboCookie(ctx context.Context, cookie string) (WeiboView, error) {
	st, err := c.weibo.SaveCookie(ctx, cookie)
	return toWeiboView(st), err
}

// ClearWeiboCookie 清除已保存的 cookie。
func (c *Core) ClearWeiboCookie() error { return c.weibo.ClearCookie() }

// CheckWeiboCookie 立即检测一次。
func (c *Core) CheckWeiboCookie(ctx context.Context) WeiboView {
	return toWeiboView(c.weibo.Check(ctx))
}

// EnvView 是状态栏要显示的运行环境。
type EnvView struct {
	Version string `json:"version"`
	Mode    string `json:"mode"`
	DataDir string `json:"dataDir"`
	Uptime  string `json:"uptime"`
}

var startedAt = time.Now()

// Uptime 返回已运行时长，供状态栏显示。
func Uptime() string {
	d := time.Since(startedAt).Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
