package tools

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ---------- 版本号解析 ----------

func TestParseVersion(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"streamlink", "streamlink 6.7.4\n", "6.7.4"},
		{"yt-dlp 日期版本号", "2024.04.09\n", "2024.04.09"},
		{"ffmpeg 发行版", "ffmpeg version 6.1.1-full_build-www.gyan.dev Copyright (c) 2000-2023\n" +
			"built with gcc 12.2.0\n", "6.1.1"},
		// git 主线构建的版本号里没有 x.y 形式，整串就是版本；
		// 若只认点分数字，会跳过它去抓下一行的 gcc 版本，显示出根本不存在的 "ffmpeg 15.2.0"
		{"ffmpeg git 构建", "ffmpeg version N-125185-g30155f9c3a-20260623 Copyright (c) 2000-2026 the FFmpeg developers\n" +
			"  built with gcc 15.2.0 (crosstool-NG 1.28.0.23_185f348)\n" +
			"  configuration: --prefix=/ffbuild/prefix\n", "N-125185-g30155f9c3a-20260623"},
		{"构建工具链版本不得泄漏", "SomeTool\n  built with gcc 15.2.0\n  compiled by clang 17.0.1\n", ""},
		{"带 V 前缀", "N_m3u8DL-RE V0.2.0-beta\n", "0.2.0"},
		{"带 commit 后缀", "0.6.0+df70f0b3da0c630bd413bf617e758051f6b64757\n", "0.6.0"},
		{"go 式前缀", "go version go1.26.6 windows/amd64\n", "1.26.6"},
		{"版本号在第二行", "Some Tool\nVersion 1.10.3\n", "1.10.3"},
		{"结尾多余的点", "tool 1.2.\n", "1.2"},
		{"无版本号", "usage: tool [options]\n", ""},
		{"空输出", "", ""},
		// 纯整数不算版本号：否则 "error code 2" 之类会被误当成版本
		{"纯整数不匹配", "exit code 2\n", ""},
		// 帮助文本里的 --version 说明行不能被当成版本声明
		{"帮助文本里的 version 字样", "  --version    show version and exit\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseVersion(c.out); got != c.want {
				t.Errorf("parseVersion(%q) = %q, 期望 %q", c.out, got, c.want)
			}
		})
	}
}

// ---------- 能力探测 ----------

const streamlinkHelp = `usage: streamlink [OPTIONS] <URL> [STREAM]
  -O, --stdout          Write stream data to stdout instead of playing it.
  -o FILENAME, --output FILENAME
  --json                Output JSON representations instead of the normal text output.
  --http-proxy HTTP_PROXY
                        A HTTP proxy to use for all HTTP requests.
  --retry-streams DELAY
`

func TestDetectCaps(t *testing.T) {
	caps := detectCaps(streamlinkHelp)
	for _, want := range []string{CapStdout, CapProxy, CapJSON} {
		if !hasCap(caps, want) {
			t.Errorf("期望检测到能力 %q，实际 %v", want, caps)
		}
	}
}

func TestDetectCapsMinimal(t *testing.T) {
	if caps := detectCaps("usage: tool [file]\n"); len(caps) != 0 {
		t.Errorf("无特征的帮助文本不应检测出能力，实际 %v", caps)
	}
}

func TestDetectCapsStdoutIsCaseSensitive(t *testing.T) {
	// -o（小写）是输出到文件，不能当成标准输出能力
	if caps := detectCaps("  -o, --output FILE   write to file\n"); hasCap(caps, CapStdout) {
		t.Errorf("小写 -o 不应被判定为标准输出能力，实际 %v", caps)
	}
}

func TestDetectCapsMatchesPrefixedFlags(t *testing.T) {
	// N_m3u8DL-RE 用的是带前缀的命名，只匹配 --proxy/--json 字面量会全部漏掉
	help := "  --write-meta-json    写入元信息\n" +
		"  --use-system-proxy   使用系统代理\n" +
		"  --custom-proxy <URL> 自定义代理\n"
	caps := detectCaps(help)
	for _, want := range []string{CapProxy, CapJSON} {
		if !hasCap(caps, want) {
			t.Errorf("带前缀的选项应识别出 %q，实际 %v", want, caps)
		}
	}
}

func TestDetectCapsIgnoresPartialWords(t *testing.T) {
	if caps := detectCaps("  --proxying-mode X\n  --jsonl-output Y\n"); len(caps) != 0 {
		t.Errorf("关键词后接其它字母不应算命中，实际 %v", caps)
	}
}

func TestCountFlags(t *testing.T) {
	// 去重后应为 --stdout --output --json --http-proxy --retry-streams 共 5 个
	if n := countFlags(streamlinkHelp); n != 5 {
		t.Errorf("countFlags = %d, 期望 5", n)
	}
}

func TestCountFlagsDeduplicates(t *testing.T) {
	if n := countFlags("--json\n--json\n--json\n"); n != 1 {
		t.Errorf("重复选项应去重，countFlags = %d, 期望 1", n)
	}
}

// ---------- 探测流程（注入假 exec）----------

type fakeCall struct {
	args []string
}

// fakeExec 记录每次调用，并按 args[0] 返回预设结果。
func fakeExec(t *testing.T, table map[string]string, fail map[string]bool) (func(context.Context, string, ...string) ([]byte, error), *[]fakeCall) {
	t.Helper()
	var calls []fakeCall
	fn := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, fakeCall{args: args})
		key := strings.Join(args, " ")
		if fail[key] {
			return nil, errors.New("exit status 1")
		}
		return []byte(table[key]), nil
	}
	return fn, &calls
}

func TestProbeFirstCandidateWins(t *testing.T) {
	fn, calls := fakeExec(t, map[string]string{
		"--version": "streamlink 6.7.4",
		"--help":    streamlinkHelp,
	}, nil)
	p := &Prober{exec: fn}

	info, err := p.Probe(context.Background(), "streamlink.exe")
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	if info.Version != "6.7.4" {
		t.Errorf("Version = %q, 期望 6.7.4", info.Version)
	}
	// --version 命中即止，不应再试 -version/-V/version
	versionAttempts := 0
	for _, c := range *calls {
		if c.args[0] != "--help" && c.args[0] != "-h" && c.args[0] != "-help" {
			versionAttempts++
		}
	}
	if versionAttempts != 1 {
		t.Errorf("版本探测调用了 %d 次，首个候选命中时应只调用 1 次", versionAttempts)
	}
}

func TestProbeFallsBackThroughCandidates(t *testing.T) {
	// 模拟 ffmpeg：--version 不认，-version 才认
	fn, calls := fakeExec(t, map[string]string{
		"-version": "ffmpeg version 6.1.1-full_build Copyright (c)",
		"--help":   "--loglevel\n--stats\n",
	}, map[string]bool{"--version": true})
	p := &Prober{exec: fn}

	info, err := p.Probe(context.Background(), "ffmpeg.exe")
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	if info.Version != "6.1.1" {
		t.Errorf("Version = %q, 期望 6.1.1", info.Version)
	}
	if len(*calls) < 2 || (*calls)[0].args[0] != "--version" || (*calls)[1].args[0] != "-version" {
		t.Errorf("候选顺序不对: %+v", *calls)
	}
}

func TestProbeAcceptsVersionDespiteNonZeroExit(t *testing.T) {
	// 有些工具打印完版本就以非 0 退出，输出可用就应当采纳
	fn := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "--version" {
			return []byte("tool 3.2.1"), errors.New("exit status 1")
		}
		return nil, errors.New("exit status 1")
	}
	p := &Prober{exec: fn}

	info, err := p.Probe(context.Background(), "tool.exe")
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	if info.Version != "3.2.1" {
		t.Errorf("Version = %q, 期望 3.2.1", info.Version)
	}
}

func TestProbeUnknownVersionIsNotAnError(t *testing.T) {
	// 探不到版本不算失败：用户仍应能把这个内核加进来用
	fn := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("no version here"), nil
	}
	p := &Prober{exec: fn}

	info, err := p.Probe(context.Background(), "tool.exe")
	if err != nil {
		t.Fatalf("探不到版本不应返回错误，实际: %v", err)
	}
	if info.Version != "" {
		t.Errorf("Version = %q, 期望空", info.Version)
	}
	if !strings.Contains(info.Summary, "未知") {
		t.Errorf("Summary 应说明版本未知，实际 %q", info.Summary)
	}
}

func TestProbeRespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fn := func(c context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, c.Err()
	}
	p := &Prober{exec: fn}

	if _, err := p.Probe(ctx, "tool.exe"); !errors.Is(err, context.Canceled) {
		t.Errorf("已取消的 context 应返回 context.Canceled，实际 %v", err)
	}
}

func TestProbeSummaryIncludesVersionAndCaps(t *testing.T) {
	fn, _ := fakeExec(t, map[string]string{
		"--version": "streamlink 6.7.4",
		"--help":    streamlinkHelp,
	}, nil)
	p := &Prober{exec: fn}

	info, _ := p.Probe(context.Background(), "streamlink.exe")
	for _, want := range []string{"6.7.4", "标准输出", "代理"} {
		if !strings.Contains(info.Summary, want) {
			t.Errorf("Summary %q 应包含 %q", info.Summary, want)
		}
	}
}

// ---------- 输出封顶（内存红线）----------

func TestLimitWriterCapsOutput(t *testing.T) {
	var sb strings.Builder
	lw := &limitWriter{w: &sb, remaining: 10}

	n, err := lw.Write([]byte("0123456789ABCDEF"))
	if err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	// 必须谎报为全部写入，否则 exec 会把截断当成 io.ErrShortWrite 让整个探测失败
	if n != 16 {
		t.Errorf("Write 返回 %d, 期望 16（对上游谎报全量以免触发 ErrShortWrite）", n)
	}
	if got := sb.String(); got != "0123456789" {
		t.Errorf("缓冲内容 = %q, 期望 %q", got, "0123456789")
	}

	// 写满之后继续写不应再增长
	if _, err := lw.Write([]byte("more")); err != nil {
		t.Fatalf("写满后 Write 失败: %v", err)
	}
	if sb.Len() != 10 {
		t.Errorf("写满后仍在增长: len = %d", sb.Len())
	}
}

func TestProbeOutputIsCapped(t *testing.T) {
	huge := strings.Repeat("x", MaxOutput*3)
	fn := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(huge), nil
	}
	p := &Prober{exec: fn}

	info, _ := p.Probe(context.Background(), "tool.exe")
	if len(info.RawHelp) > MaxOutput {
		t.Errorf("RawHelp 长度 %d 超过上限 %d", len(info.RawHelp), MaxOutput)
	}
}

// ---------- 真实进程冒烟 ----------

func TestProbeRealBinary(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("PATH 中无 go，跳过真实进程探测")
	}
	p := &Prober{Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	info, err := p.Probe(ctx, goBin)
	if err != nil {
		t.Fatalf("探测 go 失败: %v", err)
	}
	if !strings.HasPrefix(info.Version, "1.") {
		t.Errorf("Version = %q, 期望形如 1.x.y", info.Version)
	}
}

func TestProbeMissingBinary(t *testing.T) {
	p := &Prober{Timeout: 2 * time.Second}
	if _, err := p.Probe(context.Background(), "definitely-not-a-real-tool-xyz.exe"); err == nil {
		t.Error("探测不存在的可执行文件应返回错误")
	}
}

func hasCap(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}
