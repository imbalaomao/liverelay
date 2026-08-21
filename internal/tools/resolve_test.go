package tools

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/imbalaomao/liverelay/internal/config"
)

func TestResolve(t *testing.T) {
	const data = `D:\app\data`
	abs := filepath.Join(`C:\bin`, "streamlink.exe")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空路径", "", ""},
		{"绝对路径原样返回", abs, abs},
		{"相对路径锚定到数据目录", "tools/streamlink.exe", filepath.Join(data, "tools/streamlink.exe")},
		// 裸命令要留给 PATH 查找，锚定后会变成 data\ffmpeg 这个不存在的路径
		{"裸命令交给 PATH", "ffmpeg", "ffmpeg"},
		{"裸命令带扩展名也交给 PATH", "ffmpeg.exe", "ffmpeg.exe"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Resolve(data, c.in); got != c.want {
				t.Errorf("Resolve(%q) = %q, 期望 %q", c.in, got, c.want)
			}
		})
	}
}

func TestResolvedPrefersOverride(t *testing.T) {
	const data = `D:\app\data`
	tool := config.Tool{Path: "tools/streamlink.exe", PathOverride: `C:\bin\streamlink.exe`}
	if got := Resolved(data, tool); got != `C:\bin\streamlink.exe` {
		t.Errorf("Resolved = %q，覆盖路径应优先", got)
	}

	tool.PathOverride = ""
	if got := Resolved(data, tool); got != filepath.Join(data, "tools/streamlink.exe") {
		t.Errorf("Resolved = %q", got)
	}
}

func TestResolvedAnchorsRelativeOverride(t *testing.T) {
	// 用户可能填了个相对路径当覆盖路径，同样要锚定，不能相对当前工作目录解析
	const data = `D:\app\data`
	tool := config.Tool{Path: "tools/streamlink.exe", PathOverride: "custom/sl.exe"}
	if got := Resolved(data, tool); got != filepath.Join(data, "custom/sl.exe") {
		t.Errorf("Resolved = %q", got)
	}
}

func TestProbeToolWritesBackToConfig(t *testing.T) {
	c := config.Default()
	fn, _ := fakeExec(t, map[string]string{
		"--version": "streamlink 6.7.4",
		"--help":    streamlinkHelp,
	}, nil)
	p := &Prober{exec: fn}

	info, err := p.ProbeTool(context.Background(), c, `D:\app\data`, "streamlink")
	if err != nil {
		t.Fatalf("ProbeTool 失败: %v", err)
	}
	if info.Version != "6.7.4" {
		t.Errorf("Version = %q", info.Version)
	}
	got, _ := Find(c, "streamlink")
	if got.Version != "6.7.4" || got.CapSummary == "" {
		t.Errorf("探测结果未写回配置: %+v", got)
	}
}

func TestProbeToolMissingID(t *testing.T) {
	c := config.Default()
	if _, err := (&Prober{}).ProbeTool(context.Background(), c, "data", "不存在"); !errors.Is(err, ErrNotFound) {
		t.Errorf("实际 %v", err)
	}
}

func TestProbeToolFailureLeavesConfigUntouched(t *testing.T) {
	// 探测失败时不该把已有的版本号抹成空——用户看到的应该是上次的结果加一条报错
	c := config.Default()
	SetProbe(c, "streamlink", Info{Version: "6.7.4", Summary: "版本 6.7.4"})
	fn := func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("file not found")
	}
	p := &Prober{exec: fn}

	if _, err := p.ProbeTool(context.Background(), c, "data", "streamlink"); err == nil {
		t.Fatal("探测失败应返回错误")
	}
	got, _ := Find(c, "streamlink")
	if got.Version != "6.7.4" {
		t.Errorf("探测失败不应清空既有版本号，实际 %q", got.Version)
	}
}
