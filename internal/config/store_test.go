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
