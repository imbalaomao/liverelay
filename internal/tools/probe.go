// Package tools 负责内核（streamlink / yt-dlp / ffmpeg 及用户自定义工具）的
// 注册表管理与版本、能力探测。
package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxOutput 是单次子进程探测允许缓冲的输出上限（内存红线）。
// 用户可以把任意二进制指定为内核，其输出没有任何长度保证；
// 不封顶的话，一个会疯狂刷屏的程序就能把内存吃光。
const MaxOutput = 1 << 20 // 1 MiB

// defaultProbeTimeout 是单次调用的超时。挑成 10s 是因为冷启动的 Python 系工具
// （streamlink 是 Python 打包的）在机械硬盘上首次加载可能要好几秒。
const defaultProbeTimeout = 10 * time.Second

// 能力标识。这些只作提示：帮助文本格式各家千差万别，探测不到并不阻止用户使用
// 该内核——把可用的内核挡在门外比漏报一个能力糟糕得多。
const (
	CapStdout = "stdout" // 能把流写到标准输出，是管道推流的前提
	CapProxy  = "proxy"  // 能接受代理参数
	CapJSON   = "json"   // 能输出 JSON，无人值守探测开播要用
)

var capNames = map[string]string{
	CapStdout: "标准输出",
	CapProxy:  "代理",
	CapJSON:   "JSON 输出",
}

// versionArgs 是版本参数的候选，按命中概率排序。
// -version（单横线）是 ffmpeg 一系的写法，必须单列。
var versionArgs = []string{"--version", "-version", "-V", "version"}

// helpArgs 是帮助参数的候选。
var helpArgs = []string{"--help", "-h", "-help"}

// Info 是一次探测的结果。
type Info struct {
	Version string   `json:"version"`
	Caps    []string `json:"caps"`
	Flags   int      `json:"flags"`
	Summary string   `json:"summary"`
	// RawHelp 只在探测当次用于诊断，不写进配置文件（可能有 1MB）。
	RawHelp string `json:"-"`
}

// Prober 执行探测。零值可用。
type Prober struct {
	Timeout time.Duration
	// exec 供测试注入；nil 时走真实进程。
	exec func(ctx context.Context, path string, args ...string) ([]byte, error)
}

// Probe 依次尝试版本参数与帮助参数，汇总成 Info。
//
// 错误语义有意区分两件事：
//   - 二进制根本跑不起来（路径错、不是可执行文件）→ 返回 error
//   - 跑起来了但认不出版本号 → 不返回 error，Version 为空
//
// 后者不该拦住用户：小众工具不打印版本号很常见，但照样能用。
func (p *Prober) Probe(ctx context.Context, path string) (Info, error) {
	if strings.TrimSpace(path) == "" {
		return Info{}, ErrEmptyPath
	}

	var info Info
	var lastErr error
	sawOutput := false

	for _, arg := range versionArgs {
		out, err := p.capture(ctx, path, arg)
		if cerr := ctx.Err(); cerr != nil {
			return Info{}, cerr
		}
		if out != "" {
			sawOutput = true
		}
		if err != nil {
			lastErr = err
		}
		// 有些工具打印完版本就以非 0 退出，只看输出、不看退出码
		if v := parseVersion(out); v != "" {
			info.Version = v
			break
		}
	}

	for _, arg := range helpArgs {
		out, err := p.capture(ctx, path, arg)
		if cerr := ctx.Err(); cerr != nil {
			return Info{}, cerr
		}
		if err != nil {
			lastErr = err
		}
		if out != "" {
			sawOutput = true
			info.RawHelp = out
			break
		}
	}

	if !sawOutput {
		return Info{}, fmt.Errorf("无法执行 %s: %w", path, lastErr)
	}

	info.Caps = detectCaps(info.RawHelp)
	info.Flags = countFlags(info.RawHelp)
	info.Summary = info.buildSummary()
	return info, nil
}

// capture 跑一次子进程并返回被封顶的合并输出。
func (p *Prober) capture(ctx context.Context, path string, args ...string) (string, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	fn := p.exec
	if fn == nil {
		fn = RunCapture
	}
	out, err := fn(cctx, path, args...)
	// 真实路径由 limitWriter 保证不超限；注入的假 exec 走不到那里，这里兜底。
	if len(out) > MaxOutput {
		out = trimPartialRune(out[:MaxOutput])
	}
	return strings.TrimSpace(string(out)), err
}

// RunCapture 以参数数组直接起进程——不经 shell，用户填的路径与参数不会被解释成命令。
// 输出封顶 MaxOutput；超时由调用方通过 ctx 设定。
func RunCapture(ctx context.Context, path string, args ...string) ([]byte, error) {
	// #nosec G204 -- 执行用户指定的内核正是本程序的用途。
	// 参数以数组传递、不经 shell，路径来自用户在界面上的显式选择。
	cmd := exec.CommandContext(ctx, path, args...)
	var buf bytes.Buffer
	lw := &limitWriter{w: &buf, remaining: MaxOutput}
	// 版本与帮助写 stdout 还是 stderr 各家不一（ffmpeg 无参数时写 stderr），两路都收。
	// Stdout 与 Stderr 指向同一个可比较的 writer 时，exec 保证不会并发调用 Write。
	cmd.Stdout = lw
	cmd.Stderr = lw
	err := cmd.Run()
	return trimPartialRune(buf.Bytes()), err
}

// limitWriter 把写入量截断在 remaining 字节内。
type limitWriter struct {
	w         io.Writer
	remaining int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	// 始终谎报写入了全部字节：返回小于 len(p) 的值会让 exec 判定 io.ErrShortWrite，
	// 把"输出太长"变成"探测失败"，而我们只是想丢掉多余的部分。
	if l.remaining <= 0 {
		return len(p), nil
	}
	n := len(p)
	if n > l.remaining {
		n = l.remaining
	}
	if _, err := l.w.Write(p[:n]); err != nil {
		return 0, err
	}
	l.remaining -= n
	return len(p), nil
}

// versionPattern 匹配点分版本号，要求至少一个点。
// 不接受纯整数：否则 "exit code 2" 里的 2 会被当成版本号。
var versionPattern = regexp.MustCompile(`\d+\.\d+[\d.]*`)

// versionMarker 抓 "xxx version <token>" 里的 token。ffmpeg 的 git 主线构建
// 版本形如 N-125185-g30155f9c3a-20260623，压根没有点分数字，只能靠这个标记定位。
var versionMarker = regexp.MustCompile(`(?i)\bversion\b[:\s]+(\S+)`)

// buildNoise 是构建工具链信息的特征。ffmpeg 第二行的 "built with gcc 15.2.0"
// 是最典型的陷阱——不过滤就会把 gcc 的版本当成 ffmpeg 的版本显示给用户。
var buildNoise = regexp.MustCompile(`(?i)built with|compiled|configuration:|\bgcc\b|\bclang\b`)

// maxVersionLines 限定只在开头几行里找版本号，再往后基本都是构建参数。
const maxVersionLines = 5

// maxVersionLen 限定版本串长度，防止把一整行垃圾塞进 UI。
const maxVersionLen = 48

// parseVersion 从工具输出里取版本号。
func parseVersion(out string) string {
	seen := 0
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		seen++
		if seen > maxVersionLines {
			break
		}
		// 首行永远要看：ffmpeg 的版本行自带 "Copyright" 字样，会被噪声规则误伤
		if seen > 1 && buildNoise.MatchString(line) {
			continue
		}
		if v := versionFromLine(line); v != "" {
			return v
		}
	}
	return ""
}

func versionFromLine(line string) string {
	if m := versionMarker.FindStringSubmatch(line); m != nil {
		if v := normalizeVersionToken(m[1]); v != "" {
			return v
		}
	}
	// 没有 version 标记时退回直接找点分版本号（streamlink 6.7.4 / yt-dlp 2024.04.09）
	return strings.TrimRight(versionPattern.FindString(line), ".")
}

// normalizeVersionToken 把 "version" 后面那个词收拾成版本号。
func normalizeVersionToken(tok string) string {
	tok = strings.Trim(tok, `,;:)]}'"`)
	if !strings.ContainsAny(tok, "0123456789") {
		// "--version    show version" 这类帮助文本会把 "show" 送进来
		return ""
	}
	if loc := versionPattern.FindStringIndex(tok); loc != nil && isShortAlpha(tok[:loc[0]]) {
		// go1.26.6 / V0.2.0-beta / 6.1.1-full_build → 取点分部分
		return strings.TrimRight(tok[loc[0]:loc[1]], ".")
	}
	// 认不出点分版本号就整串当版本用（ffmpeg 的 N-xxxxx-gxxxxxxx 构建号）
	if len(tok) > maxVersionLen {
		tok = string(trimPartialRune([]byte(tok[:maxVersionLen])))
	}
	return tok
}

// isShortAlpha 判断数字前的那截前缀是不是可丢弃的字母标记（go、v、V）。
// 前缀里一旦出现数字或符号，说明这串点分数字是嵌在别的东西里，不能单独拎出来。
func isShortAlpha(s string) bool {
	if len(s) > 3 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// capPatterns 用"任意前缀 + 关键词"匹配长选项，因为各家命名差异很大：
// --proxy / --http-proxy / --custom-proxy / --use-system-proxy 说的是同一件事。
// 全部大小写敏感：帮助文本里的长选项一律小写，而 -O 与 -o 在 streamlink 里
// 是截然不同的两个选项（大写才是标准输出），大小写不敏感会把它们混为一谈。
var capPatterns = []struct {
	cap string
	re  *regexp.Regexp
}{
	// 注：yt-dlp 的 -O 其实是 --print 的简写，会在这里误判成标准输出能力。
	// 结论恰好是对的（yt-dlp 用 -o - 输出到标准输出），且能力仅作提示不作门禁，故不再细分。
	{CapStdout, regexp.MustCompile(`--[a-z0-9-]*stdout\b|-O,|-O |pipe:1`)},
	{CapProxy, regexp.MustCompile(`--[a-z0-9-]*proxy\b`)},
	{CapJSON, regexp.MustCompile(`--[a-z0-9-]*json\b`)},
}

// detectCaps 从帮助文本里识别我们真正会用到的几项能力。
func detectCaps(help string) []string {
	var caps []string
	for _, p := range capPatterns {
		if p.re.MatchString(help) {
			caps = append(caps, p.cap)
		}
	}
	return caps
}

var flagPattern = regexp.MustCompile(`--[a-zA-Z][a-zA-Z0-9-]*`)

// countFlags 数帮助文本里出现过多少种长选项，用来给用户一个"这工具有多复杂"的直观印象。
func countFlags(help string) int {
	seen := make(map[string]struct{})
	for _, f := range flagPattern.FindAllString(help, -1) {
		seen[f] = struct{}{}
	}
	return len(seen)
}

func (i Info) buildSummary() string {
	var b strings.Builder
	if i.Version != "" {
		b.WriteString("版本 " + i.Version)
	} else {
		b.WriteString("版本未知")
	}
	if len(i.Caps) > 0 {
		names := make([]string, 0, len(i.Caps))
		for _, c := range i.Caps {
			if n, ok := capNames[c]; ok {
				names = append(names, n)
			}
		}
		b.WriteString("；支持 " + strings.Join(names, "/"))
	}
	if i.Flags > 0 {
		fmt.Fprintf(&b, "；共 %d 个选项", i.Flags)
	}
	return b.String()
}

// trimPartialRune 去掉结尾被截断的半个 UTF-8 字符，避免中文帮助文本尾部出现乱码。
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0; i++ {
		r, size := utf8.DecodeLastRune(b)
		if r != utf8.RuneError || size > 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}
