package pipeline

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/imbalaomao/liverelay/internal/config"
)

func baseOpts() Options {
	return Options{
		Task: config.Task{
			ID: "t1", Name: "测试/台", SourceURL: "https://x/1", Quality: "best",
			Targets: []config.Target{
				{Proto: "rtmp", URL: "rtmp://a/live", Key: "k1"},
				{Proto: "srt", URL: "srt://s:9000"},
				{Proto: "hls", URL: "out/index.m3u8"},
			},
		},
		FetchTool:  config.Tool{ID: "streamlink", Builtin: true, ArgTemplate: []string{"{url}", "{quality}", "-O"}},
		FFmpegPath: "ffmpeg",
		DataDir:    "data",
	}
}

func TestBuildFFmpegArgsMultiTarget(t *testing.T) {
	got, err := buildFFmpegArgs(baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"-c copy -f flv rtmp://a/live/k1",
		"-c copy -f mpegts srt://s:9000",
		"-c copy -f hls -hls_time 4 -hls_list_size 6 out/index.m3u8",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("ffmpeg 参数缺少 %q\n实际: %s", want, joined)
		}
	}
}

func TestBuildFFmpegArgsRecord(t *testing.T) {
	o := baseOpts()
	o.Record = true
	got, err := buildFFmpegArgs(o)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-f segment -segment_time 1800 -strftime 1") {
		t.Fatalf("缺少录制分段参数: %s", joined)
	}
	if !strings.Contains(joined, "测试_台") {
		t.Fatalf("录制目录应使用消毒后的任务名: %s", joined)
	}
}

func TestBuildFFmpegArgsUnknownProto(t *testing.T) {
	o := baseOpts()
	o.Task.Targets = []config.Target{{Proto: "rtsp", URL: "rtmp://x"}}
	if _, err := buildFFmpegArgs(o); err == nil {
		t.Fatal("未知协议应报错")
	}
}

func TestBuildFetchArgsCustomOnly(t *testing.T) {
	o := baseOpts()
	o.Task.CustomArgs = "--foo \"a b\""
	// 内置工具：自定义参数被忽略
	got, err := buildFetchArgs(o)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://x/1", "best", "-O"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("内置工具不应附加自定义参数: %v", got)
	}
	// 自定义工具：逐字追加
	o.FetchTool.Builtin = false
	got, err = buildFetchArgs(o)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"https://x/1", "best", "-O", "--foo", "a b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("自定义参数未逐字追加: %v", got)
	}
}

// 未 Start 就 Wait 不得 panic（Runner 是导出类型，调用顺序不受本包控制）。
func TestWaitBeforeStartReturnsError(t *testing.T) {
	info := NewRunner(baseOpts()).Wait()
	if info.Normal || info.Err == nil {
		t.Fatalf("未启动时 Wait 应返回错误，得到 %+v", info)
	}
}

func TestStopBeforeStartIsSafe(t *testing.T) {
	if err := NewRunner(baseOpts()).Stop(); err != nil {
		t.Fatalf("未启动时 Stop 应无副作用，得到 %v", err)
	}
}

// ffmpeg 先死（如推流密钥错误）会连带把抓流进程写崩，两个进程同时报错。
// 此时必须暴露 ffmpeg 的真实原因，否则用户只看到"抓流进程异常退出"而无从排查。
func TestExitInfoSurfacesFFmpegCauseWhenBothFail(t *testing.T) {
	r := &Runner{
		fetchLog: &limitWriter{max: 1024},
		ffLog:    &limitWriter{max: 1024},
	}
	r.fetchLog.Write([]byte("broken pipe"))
	r.ffLog.Write([]byte("RTMP_Connect0, failed to connect socket"))

	info := r.exitInfo(errors.New("exit status 1"), errors.New("exit status 1"))
	if info.Normal {
		t.Fatal("双进程报错时不应判为正常结束")
	}
	if !strings.Contains(info.Err.Error(), "RTMP_Connect0") {
		t.Fatalf("错误信息应包含 ffmpeg 真实原因，得到: %v", info.Err)
	}
}

func TestExitInfoNormalWhenBothClean(t *testing.T) {
	r := &Runner{fetchLog: &limitWriter{max: 1024}, ffLog: &limitWriter{max: 1024}}
	if info := r.exitInfo(nil, nil); !info.Normal {
		t.Fatalf("双进程正常退出应判为 Normal，得到 %+v", info)
	}
}

// tail 按字节截断会切坏多字节字符，错误信息在 UI 上显示为乱码首字符。
func TestTailKeepsValidUTF8(t *testing.T) {
	w := &limitWriter{max: 64 * 1024}
	w.Write([]byte(strings.Repeat("中文错误信息", 100)))
	got := tail(w)
	if !utf8.ValidString(got) {
		t.Fatalf("tail 输出不是合法 UTF-8: %q", got)
	}
	if len(got) > 200 {
		t.Fatalf("tail 输出应不超过 200 字节，得到 %d", len(got))
	}
}
