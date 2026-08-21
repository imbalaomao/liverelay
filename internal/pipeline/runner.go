package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/core"
	"github.com/imbalaomao/liverelay/internal/tools"
)

// Options 装配一条直通管道所需的全部输入。
type Options struct {
	Task       config.Task
	FetchTool  config.Tool
	FFmpegPath string
	DataDir    string
	ProxyURL   string // 渲染好的代理 URL（如 http://127.0.0.1:7890），空表示无代理
	Record     bool   // 本次运行是否录制（手动按钮或 AutoRecord 触发）
	// CookieFile 是 Netscape 格式的 YouTube cookies.txt，仅在 yt-dlp 抓
	// YouTube 时注入——那种组合几乎必然撞上人机验证。
	CookieFile string
}

func buildFetchArgs(o Options) ([]string, error) {
	vars := map[string]string{
		"url":     o.Task.SourceURL,
		"quality": o.Task.Quality,
		"proxy":   o.ProxyURL,
		"outdir":  filepath.Join(o.DataDir, "recordings", SanitizeName(o.Task.Name)),
	}
	args := RenderTemplate(o.FetchTool.ArgTemplate, vars)
	// 只在确实需要时注入：streamlink 不认这个参数，而给无关站点带上
	// YouTube 的登录态更是不该发生的事
	if o.CookieFile != "" && tools.IsYtdlpFamily(o.FetchTool) && tools.IsYouTubeURL(o.Task.SourceURL) {
		args = append(args, "--cookies", o.CookieFile)
	}
	if !o.FetchTool.Builtin && strings.TrimSpace(o.Task.CustomArgs) != "" {
		extra, err := Tokenize(o.Task.CustomArgs)
		if err != nil {
			return nil, err
		}
		args = append(args, extra...)
	}
	return args, nil
}

func buildFFmpegArgs(o Options) ([]string, error) {
	args := []string{"-hide_banner", "-loglevel", "warning", "-i", "pipe:0"}
	for _, t := range o.Task.Targets {
		args = append(args, "-c", "copy")
		switch t.Proto {
		case "rtmp":
			push := t.URL
			if t.Key != "" {
				push = strings.TrimSuffix(t.URL, "/") + "/" + t.Key
			}
			args = append(args, "-f", "flv", push)
		case "srt", "udp":
			args = append(args, "-f", "mpegts", t.URL)
		case "hls":
			args = append(args, "-f", "hls", "-hls_time", "4", "-hls_list_size", "6", t.URL)
		default:
			return nil, fmt.Errorf("未知推流协议 %q", t.Proto)
		}
	}
	if o.Record {
		dir := filepath.Join(o.DataDir, "recordings", SanitizeName(o.Task.Name))
		args = append(args, "-c", "copy", "-f", "segment", "-segment_time", "1800",
			"-strftime", "1", filepath.Join(dir, "%Y%m%d_%H%M%S.mp4"))
	}
	return args, nil
}

// limitWriter 上限字节缓冲：超出部分静默丢弃，防止 stderr 撑爆内存。
type limitWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if room := w.max - w.buf.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		w.buf.Write(p)
	}
	return len(p), nil
}

func (w *limitWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func tail(w *limitWriter) string {
	s := w.String()
	if len(s) > 200 {
		s = s[len(s)-200:]
		// 按字节截断可能切在多字节字符中间，丢掉开头的残字节保证合法 UTF-8
		for len(s) > 0 && !utf8.RuneStart(s[0]) {
			s = s[1:]
		}
	}
	return strings.TrimSpace(s)
}

// Runner 实现 core.Runner：抓流进程 stdout 直通 ffmpeg stdin（OS 管道，零 Go 堆拷贝）。
type Runner struct {
	opts     Options
	fetch    *exec.Cmd
	ff       *exec.Cmd
	fetchLog *limitWriter
	ffLog    *limitWriter
}

func NewRunner(o Options) *Runner { return &Runner{opts: o} }

func (r *Runner) Start(ctx context.Context) error {
	fetchArgs, err := buildFetchArgs(r.opts)
	if err != nil {
		return err
	}
	ffArgs, err := buildFFmpegArgs(r.opts)
	if err != nil {
		return err
	}
	if r.opts.Record {
		_ = os.MkdirAll(filepath.Join(r.opts.DataDir, "recordings", SanitizeName(r.opts.Task.Name)), 0o750)
	}
	// #nosec G204 -- 拉起用户指定的内核与 ffmpeg 正是本程序的用途。
	// 三重约束保证这不是命令注入面：可执行文件路径来自用户在界面上的显式选择；
	// 参数以数组逐个传递、绝不拼接成命令行；全程不经 shell。
	r.ff = exec.CommandContext(ctx, r.opts.FFmpegPath, ffArgs...)
	// #nosec G204 -- 同上：路径来自用户选定的内核，参数走数组不经 shell
	r.fetch = exec.CommandContext(ctx, r.opts.FetchTool.EffectivePath(), fetchArgs...)
	if r.opts.ProxyURL != "" {
		env := append(os.Environ(), "http_proxy="+r.opts.ProxyURL, "https_proxy="+r.opts.ProxyURL)
		r.ff.Env = env
		r.fetch.Env = env
	}
	src, err := r.fetch.StdoutPipe()
	if err != nil {
		return err
	}
	r.ff.Stdin = src
	r.fetchLog = &limitWriter{max: 64 * 1024}
	r.ffLog = &limitWriter{max: 64 * 1024}
	r.fetch.Stderr = r.fetchLog
	r.ff.Stderr = r.ffLog
	if err := r.ff.Start(); err != nil {
		return fmt.Errorf("启动 ffmpeg: %w", err)
	}
	if err := r.fetch.Start(); err != nil {
		// 必须 Kill + Wait 双管齐下：只 Kill 会留下未回收的子进程和
		// CommandContext 内部的 watch goroutine（资源红线）。
		_ = r.ff.Process.Kill()
		_ = r.ff.Wait()
		r.ff, r.fetch = nil, nil
		return fmt.Errorf("启动抓流工具: %w", err)
	}
	return nil
}

// exitInfo 把两个子进程的退出状态合成一条结论。
// ffmpeg 先死（如推流密钥错误）会连带写崩抓流进程，两者同时报错——
// 此时必须暴露 ffmpeg 的原因，否则用户只看到"抓流进程异常退出"而无从排查。
func (r *Runner) exitInfo(ferr, fferr error) core.ExitInfo {
	switch {
	case ferr == nil && fferr == nil:
		return core.ExitInfo{Normal: true}
	case fferr != nil && ferr != nil:
		return core.ExitInfo{Err: fmt.Errorf("ffmpeg 异常退出: %v (%s)；抓流进程随之退出: %v (%s)",
			fferr, tail(r.ffLog), ferr, tail(r.fetchLog))}
	case fferr != nil:
		return core.ExitInfo{Err: fmt.Errorf("ffmpeg 异常退出: %v (%s)", fferr, tail(r.ffLog))}
	default:
		return core.ExitInfo{Err: fmt.Errorf("抓流进程异常退出: %v (%s)", ferr, tail(r.fetchLog))}
	}
}

func (r *Runner) Wait() core.ExitInfo {
	if r.fetch == nil || r.ff == nil {
		return core.ExitInfo{Err: errors.New("管道尚未启动")}
	}
	ferr := r.fetch.Wait()
	fferr := r.ff.Wait()
	return r.exitInfo(ferr, fferr)
}

func (r *Runner) Stop() error {
	if r.fetch != nil && r.fetch.Process != nil {
		_ = r.fetch.Process.Kill()
	}
	if r.ff != nil && r.ff.Process != nil {
		_ = r.ff.Process.Kill()
	}
	return nil
}
