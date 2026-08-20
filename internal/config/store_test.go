package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Version != 1 || c.Settings.MaxConcurrent != 4 || len(c.Tools) != 3 {
		t.Fatalf("默认配置不符: %+v", c)
	}
	if !c.Tools[0].Builtin || c.Tools[0].ID != "streamlink" {
		t.Fatalf("内置工具缺失: %+v", c.Tools[0])
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c := Default()
	c.Tasks = append(c.Tasks, Task{
		ID: "t1", Name: "测试台", SourceURL: "https://x/live/1", ToolID: "streamlink",
		Quality: "best", CustomArgs: "--foo \"a b\"",
		Targets: []Target{{Proto: "rtmp", URL: "rtmp://a/b", Key: "k1"}, {Proto: "srt", URL: "srt://s:9000"}},
	})
	if err := Save(p, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].CustomArgs != "--foo \"a b\"" || len(got.Tasks[0].Targets) != 2 {
		t.Fatalf("往返不一致: %+v", got.Tasks)
	}
}

func TestLoadCorruptFallsBackToBak(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	first := Default()
	first.Tasks = append(first.Tasks, Task{ID: "v1", Name: "第一版"})
	if err := Save(p, first); err != nil {
		t.Fatal(err)
	}
	second := Default()
	second.Tasks = append(second.Tasks, Task{ID: "v2", Name: "第二版"})
	if err := Save(p, second); err != nil { // 此时第一版进入 .bak
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{损坏"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("应回退 .bak 而非报错: %v", err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].ID != "v1" {
		t.Fatalf("应恢复到第一版: %+v", got.Tasks)
	}
}

func TestEffectivePath(t *testing.T) {
	tool := Tool{Path: "tools/a.exe", PathOverride: `D:\bin\a.exe`}
	if tool.EffectivePath() != `D:\bin\a.exe` {
		t.Fatal("覆盖路径未生效")
	}
	tool.PathOverride = ""
	if tool.EffectivePath() != "tools/a.exe" {
		t.Fatal("默认路径未生效")
	}
}

// 并发上限与探测间隔必须双向钳制：上限失控会导致数十个 ffmpeg 同时运行，
// 直接违反资源红线；探测间隔过小会高频请求直播平台。
func TestParseClampsSettings(t *testing.T) {
	cases := []struct {
		name                 string
		json                 string
		wantMax, wantProbeIS int
	}{
		{"零值取默认", `{"settings":{"maxConcurrent":0,"probeIntervalSec":0}}`, 4, 60},
		{"负值取默认", `{"settings":{"maxConcurrent":-3,"probeIntervalSec":-1}}`, 4, 60},
		{"超上限被钳制", `{"settings":{"maxConcurrent":9999,"probeIntervalSec":86400}}`, 16, 300},
		{"合法值保留", `{"settings":{"maxConcurrent":8,"probeIntervalSec":120}}`, 8, 120},
		{"边界值保留", `{"settings":{"maxConcurrent":16,"probeIntervalSec":30}}`, 16, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := parse([]byte(tc.json))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if c.Settings.MaxConcurrent != tc.wantMax {
				t.Errorf("MaxConcurrent = %d，期望 %d", c.Settings.MaxConcurrent, tc.wantMax)
			}
			if c.Settings.ProbeIntervalSec != tc.wantProbeIS {
				t.Errorf("ProbeIntervalSec = %d，期望 %d", c.Settings.ProbeIntervalSec, tc.wantProbeIS)
			}
		})
	}
}

func TestLoadToleratesUTF8BOM(t *testing.T) {
	// 记事本、VS Code 的"UTF-8 with BOM"、PowerShell 的 Set-Content -Encoding UTF8
	// 都会写出带 BOM 的文件。json.Unmarshal 见到 BOM 直接报错，
	// 结果是用户手改一次配置就被静默重置回默认值。
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"version":1,"settings":{"closeToTray":false,"maxConcurrent":3,"probeIntervalSec":90}}`
	if err := os.WriteFile(path, append([]byte("\xef\xbb\xbf"), body...), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("带 BOM 的配置应能正常读取: %v", err)
	}
	if c.Settings.CloseToTray {
		t.Error("closeToTray 应为 false —— 配置被回退成默认值了")
	}
	if c.Settings.MaxConcurrent != 3 || c.Settings.ProbeIntervalSec != 90 {
		t.Errorf("配置未正确读取: %+v", c.Settings)
	}
}

func TestLoadToleratesLeadingWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("\r\n  {\"version\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("前导空白不应导致解析失败: %v", err)
	}
}

func TestDefaultProxyTypeIsValid(t *testing.T) {
	// 默认留空会让设置页的下拉框选不中任何一项，看起来像坏了
	c := Default()
	if c.Settings.Proxy.Type != "http" {
		t.Errorf("默认代理类型 = %q，应是 http 或 socks5 之一", c.Settings.Proxy.Type)
	}
}

func TestParseNormalisesProxyType(t *testing.T) {
	// 老配置里可能是空的或写错的，读进来要归一化——
	// 否则设置页的下拉框选不中任何一项
	dir := t.TempDir()
	for _, in := range []string{`""`, `"HTTP"`, `"什么协议"`, `"socks5"`} {
		path := filepath.Join(dir, "c.json")
		body := `{"version":1,"settings":{"proxy":{"type":` + in + `}}}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		got := c.Settings.Proxy.Type
		if got != "http" && got != "socks5" {
			t.Errorf("输入 %s 归一化成 %q，不是合法类型", in, got)
		}
	}
}
