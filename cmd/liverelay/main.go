package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/core"
	"github.com/imbalaomao/liverelay/internal/pipeline"
)

// resolve 把配置中的相对内核路径锚定到数据目录；绝对路径与 PATH 中的裸命令原样返回。
func resolve(dataDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	if !filepath.IsAbs(p) && filepath.Base(p) != p {
		return filepath.Join(dataDir, p)
	}
	return p
}

// findTool 按 ID 查内核，避免依赖 Tools 切片顺序（用户可编辑 config.json）。
func findTool(cfg *config.Config, id string) (config.Tool, bool) {
	for _, t := range cfg.Tools {
		if t.ID == id {
			return t, true
		}
	}
	return config.Tool{}, false
}

// M2 headless 冒烟入口：liverelay -source <直播源URL> -target <rtmp地址> [-key 密钥]
func main() {
	dataDir := flag.String("data", "data", "数据目录")
	source := flag.String("source", "", "直播源 URL")
	target := flag.String("target", "", "RTMP 推流地址")
	key := flag.String("key", "", "推流密钥")
	record := flag.Bool("record", false, "同步录制")
	toolID := flag.String("tool", "streamlink", "抓流内核 ID")
	flag.Parse()

	if *source == "" || *target == "" {
		fmt.Println("用法: liverelay -source <直播源URL> -target <rtmp地址> [-key 密钥] [-record] [-tool 内核ID] [-data 数据目录]")
		os.Exit(2)
	}

	cfg, err := config.Load(filepath.Join(*dataDir, "config.json"))
	if err != nil {
		log.Fatalf("加载配置: %v", err)
	}

	ffmpeg := filepath.Join(*dataDir, "tools", "ffmpeg.exe")
	if _, err := os.Stat(ffmpeg); err != nil {
		p, lerr := exec.LookPath("ffmpeg")
		if lerr != nil {
			log.Fatal("未找到 ffmpeg：请放入 data/tools/ 或加入 PATH")
		}
		ffmpeg = p
	}

	tool, ok := findTool(cfg, *toolID)
	if !ok {
		log.Fatalf("配置中不存在内核 %s", *toolID)
	}
	tool.Path = resolve(*dataDir, tool.Path)
	tool.PathOverride = resolve(*dataDir, tool.PathOverride)
	if _, err := exec.LookPath(tool.EffectivePath()); err != nil {
		log.Fatalf("未找到 %s（%s）：请放入 %s/tools/ 或加入 PATH", tool.Name, tool.EffectivePath(), *dataDir)
	}

	task := config.Task{
		ID: "cli", Name: "cli", SourceURL: *source, ToolID: tool.ID, Quality: "best",
		Targets: []config.Target{{Proto: "rtmp", URL: *target, Key: *key}},
	}
	cfg.Tasks = append(cfg.Tasks, task)

	m := core.NewManager(cfg, func(t config.Task) core.Runner {
		return pipeline.NewRunner(pipeline.Options{
			Task: t, FetchTool: tool, FFmpegPath: ffmpeg, DataDir: *dataDir, Record: *record,
		})
	}, func(ev core.Event) {
		log.Printf("[%s] %-12s %s", ev.TaskID, ev.State, pipeline.Redact(ev.Msg, []string{*key}))
	})

	if err := m.Start("cli"); err != nil {
		log.Fatal(err)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("正在停止…")
	_ = m.Stop("cli")
	time.Sleep(time.Second)
}
