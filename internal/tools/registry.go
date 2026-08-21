package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/imbalaomao/liverelay/internal/config"
)

var (
	ErrNotFound        = errors.New("内核不存在")
	ErrDuplicateID     = errors.New("内核 ID 已存在")
	ErrInvalidID       = errors.New("内核 ID 非法")
	ErrEmptyPath       = errors.New("内核路径不能为空")
	ErrInvalidRole     = errors.New("内核用途非法")
	ErrBuiltinReadOnly = errors.New("内置内核不可编辑或删除")
	ErrInUse           = errors.New("内核正被任务引用")
)

// idPattern 限定 ID 为 ASCII 字母数字加短横下划线，长度不超过 64。
// ID 会出现在配置文件与日志里，放开中文和空格只会在排查问题时添乱；
// 显示名（Name）不受此限制，用户想叫什么都行。
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

var validRoles = map[string]bool{"fetch": true, "record": true, "both": true}

// Find 按 ID 查内核，返回副本。
func Find(c *config.Config, id string) (config.Tool, bool) {
	if i := indexOf(c, id); i >= 0 {
		return c.Tools[i], true
	}
	return config.Tool{}, false
}

func indexOf(c *config.Config, id string) int {
	for i := range c.Tools {
		if c.Tools[i].ID == id {
			return i
		}
	}
	return -1
}

func validate(t config.Tool) error {
	if !idPattern.MatchString(t.ID) {
		return fmt.Errorf("%w：%q（限 ASCII 字母数字与 - _，不超过 64 字符）", ErrInvalidID, t.ID)
	}
	if strings.TrimSpace(t.Path) == "" {
		return ErrEmptyPath
	}
	if !validRoles[t.Role] {
		return fmt.Errorf("%w：%q（应为 fetch / record / both）", ErrInvalidRole, t.Role)
	}
	return nil
}

// Add 新增一个自定义内核。
func Add(c *config.Config, t config.Tool) error {
	if err := validate(t); err != nil {
		return err
	}
	if indexOf(c, t.ID) >= 0 {
		return fmt.Errorf("%w：%s", ErrDuplicateID, t.ID)
	}
	// 强制置假：若信了调用方传来的 builtin:true，这个内核就再也删不掉了。
	t.Builtin = false
	c.Tools = append(c.Tools, t)
	return nil
}

// Update 修改一个自定义内核。内置内核走 SetOverride 改路径，不走这里。
func Update(c *config.Config, t config.Tool) error {
	i := indexOf(c, t.ID)
	if i < 0 {
		return fmt.Errorf("%w：%s", ErrNotFound, t.ID)
	}
	if c.Tools[i].Builtin {
		return fmt.Errorf("%w：%s", ErrBuiltinReadOnly, t.ID)
	}
	if err := validate(t); err != nil {
		return err
	}

	old := c.Tools[i]
	t.Builtin = false
	// 探测结果由 Probe 写入，编辑表单不会回传；路径没变就原样留着，
	// 变了就作废——换了路径就是换了个二进制，旧版本号会误导用户。
	if t.Path == old.Path && t.PathOverride == old.PathOverride {
		t.Version, t.CapSummary = old.Version, old.CapSummary
	} else {
		t.Version, t.CapSummary = "", ""
	}
	c.Tools[i] = t
	return nil
}

// Delete 删除一个自定义内核。内置内核与被任务引用的内核不可删。
func Delete(c *config.Config, id string) error {
	i := indexOf(c, id)
	if i < 0 {
		return fmt.Errorf("%w：%s", ErrNotFound, id)
	}
	if c.Tools[i].Builtin {
		return fmt.Errorf("%w：%s", ErrBuiltinReadOnly, id)
	}
	if names := UsedBy(c, id); len(names) > 0 {
		// 指名道姓，否则用户面对一堆任务不知道该先改哪个
		return fmt.Errorf("%w：%s", ErrInUse, strings.Join(names, "、"))
	}
	c.Tools = append(c.Tools[:i], c.Tools[i+1:]...)
	return nil
}

// SetOverride 为内核指定用户本地的可执行文件路径。
// 传入空白等同 ResetOverride。
func SetOverride(c *config.Config, id, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return ResetOverride(c, id)
	}
	i := indexOf(c, id)
	if i < 0 {
		return fmt.Errorf("%w：%s", ErrNotFound, id)
	}
	if c.Tools[i].EffectivePath() != path {
		clearProbe(&c.Tools[i])
	}
	// 只动 PathOverride，Path 保持不变——否则"恢复默认"就没有默认可恢复了
	c.Tools[i].PathOverride = path
	return nil
}

// ResetOverride 清除路径覆盖，回到内置的默认路径。
func ResetOverride(c *config.Config, id string) error {
	i := indexOf(c, id)
	if i < 0 {
		return fmt.Errorf("%w：%s", ErrNotFound, id)
	}
	if c.Tools[i].PathOverride != "" {
		clearProbe(&c.Tools[i])
	}
	c.Tools[i].PathOverride = ""
	return nil
}

// SetProbe 把一次探测的结果落到内核条目上。
func SetProbe(c *config.Config, id string, info Info) {
	if i := indexOf(c, id); i >= 0 {
		c.Tools[i].Version = info.Version
		c.Tools[i].CapSummary = info.Summary
	}
}

func clearProbe(t *config.Tool) {
	t.Version, t.CapSummary = "", ""
}

// UsedBy 返回引用了该内核的任务名（同一任务只计一次）。
func UsedBy(c *config.Config, id string) []string {
	var names []string
	for _, t := range c.Tasks {
		if t.ToolID != id && t.RecordToolID != id {
			continue
		}
		name := t.Name
		if name == "" {
			name = t.ID
		}
		names = append(names, name)
	}
	return names
}

// Resolve 把配置里的内核路径锚定到数据目录。
//
// 三种写法区别对待：
//   - 绝对路径：原样返回
//   - 含目录分隔符的相对路径（tools/streamlink.exe）：锚定到数据目录，
//     否则会相对进程当前工作目录解析——用户从哪儿双击启动结果就变一次
//   - 裸命令（ffmpeg）：原样返回，交给 PATH 查找
func Resolve(dataDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	if filepath.Base(p) != p {
		return filepath.Join(dataDir, p)
	}
	return p
}

// Resolved 返回该内核实际应当执行的路径。
func Resolved(dataDir string, t config.Tool) string {
	return Resolve(dataDir, t.EffectivePath())
}

// ProbeTool 解析内核路径、执行探测，并把结果写回配置。
// 探测失败时保留上一次的结果——让用户看到旧版本号加一条报错，
// 比把版本栏清空、显得内核凭空消失要好。
func (p *Prober) ProbeTool(ctx context.Context, c *config.Config, dataDir, id string) (Info, error) {
	t, ok := Find(c, id)
	if !ok {
		return Info{}, fmt.Errorf("%w：%s", ErrNotFound, id)
	}
	info, err := p.Probe(ctx, Resolved(dataDir, t))
	if err != nil {
		return Info{}, err
	}
	SetProbe(c, id, info)
	return info, nil
}
