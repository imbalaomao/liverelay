package pipeline

import (
	"reflect"
	"strings"
	"testing"

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
