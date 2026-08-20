package pipeline_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/pipeline"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestEndToEndRTMP(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg 不在 PATH，跳过端到端测试")
	}
	port := freePort(t)
	rtmpURL := fmt.Sprintf("rtmp://127.0.0.1:%d/live/test", port)
	out := filepath.Join(t.TempDir(), "out.flv")

	// 本地 RTMP 接收端（ffmpeg -listen 充当迷你服务器）
	sink := exec.Command(ff, "-hide_banner", "-loglevel", "error",
		"-listen", "1", "-i", rtmpURL, "-c", "copy", "-y", out)
	if err := sink.Start(); err != nil {
		t.Fatalf("启动接收端: %v", err)
	}
	defer func() { _ = sink.Process.Kill() }()
	time.Sleep(500 * time.Millisecond) // 等待监听就绪

	// 模拟抓流工具：5 秒测试流输出到 stdout（flv1/aac 为 ffmpeg 原生编码器，全构建可用）
	fetchTool := config.Tool{
		ID: "sim", Name: "sim", Path: ff,
		ArgTemplate: []string{
			"-hide_banner", "-loglevel", "error",
			"-re", "-f", "lavfi", "-i", "testsrc=duration=5:size=320x240:rate=10",
			"-re", "-f", "lavfi", "-i", "sine=frequency=440:duration=5",
			"-c:v", "flv1", "-c:a", "aac", "-f", "flv", "-",
		},
	}
	task := config.Task{
		ID: "e2e", Name: "e2e", SourceURL: "sim://local",
		Targets: []config.Target{{Proto: "rtmp", URL: rtmpURL}},
	}
	r := pipeline.NewRunner(pipeline.Options{Task: task, FetchTool: fetchTool, FFmpegPath: ff})
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	info := r.Wait()
	if !info.Normal {
		t.Fatalf("管道异常: %v", info.Err)
	}
	_ = sink.Wait()

	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("接收文件不存在: %v", err)
	}
	if st.Size() < 10*1024 {
		t.Fatalf("接收文件过小: %d 字节", st.Size())
	}

	// 内存红线粗查：Go 堆与流量无关
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapAlloc > 50*1024*1024 {
		t.Fatalf("Go 堆占用异常: %d MB", ms.HeapAlloc/1024/1024)
	}
}
