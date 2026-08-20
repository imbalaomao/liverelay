package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ---------- 下载 ----------

func TestDownloadWritesFile(t *testing.T) {
	body := []byte("这是一个假的可执行文件")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client()}
	dst := filepath.Join(t.TempDir(), "dl.bin")
	if err := u.download(context.Background(), srv.URL, dst, nil); err != nil {
		t.Fatalf("download 失败: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("内容不一致: %q", got)
	}
}

func TestDownloadRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client()}
	dst := filepath.Join(t.TempDir(), "dl.bin")
	if err := u.download(context.Background(), srv.URL, dst, nil); err == nil {
		t.Error("HTTP 404 应报错")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("失败时不应留下半个文件")
	}
}

func TestDownloadCapsSize(t *testing.T) {
	// 服务端谎报大小或无限吐数据时，必须有个硬上限把磁盘保住
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := bytes.Repeat([]byte("x"), 64*1024)
		for i := 0; i < 64; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client(), MaxDownloadBytes: 128 * 1024}
	dst := filepath.Join(t.TempDir(), "dl.bin")
	err := u.download(context.Background(), srv.URL, dst, nil)
	if err == nil {
		t.Fatal("超过上限应报错")
	}
	if !strings.Contains(err.Error(), "上限") {
		t.Errorf("错误信息应说明是超限: %v", err)
	}
	if _, serr := os.Stat(dst); !os.IsNotExist(serr) {
		t.Error("超限时不应留下半个文件")
	}
}

func TestDownloadVerifiesChecksum(t *testing.T) {
	body := []byte("正确的内容")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client()}
	dst := filepath.Join(t.TempDir(), "dl.bin")

	want := sha256Hex(body)
	if err := u.download(context.Background(), srv.URL, dst, &want); err != nil {
		t.Fatalf("校验和正确时不应报错: %v", err)
	}
}

func TestDownloadRejectsChecksumMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("被掉包的内容"))
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client()}
	dst := filepath.Join(t.TempDir(), "dl.bin")
	want := sha256Hex([]byte("正确的内容"))

	err := u.download(context.Background(), srv.URL, dst, &want)
	if err == nil {
		t.Fatal("校验和不匹配必须拒绝")
	}
	// 校验失败的文件绝不能留在盘上——留着就有被当成内核执行的风险
	if _, serr := os.Stat(dst); !os.IsNotExist(serr) {
		t.Error("校验失败的文件必须删除")
	}
}

// ---------- 解压 ----------

func makeZip(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractFindsNestedBinary(t *testing.T) {
	// ffmpeg 与 streamlink 的压缩包都是带一层顶层目录的
	zipPath := makeZip(t, map[string][]byte{
		"ffmpeg-n7.1-win64/bin/ffmpeg.exe": []byte("FFMPEG"),
		"ffmpeg-n7.1-win64/bin/ffplay.exe": []byte("FFPLAY"),
		"ffmpeg-n7.1-win64/README.txt":     []byte("readme"),
	})
	dst := filepath.Join(t.TempDir(), "out.exe")

	if err := extractBinary(zipPath, "ffmpeg.exe", dst); err != nil {
		t.Fatalf("extractBinary 失败: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "FFMPEG" {
		t.Errorf("解出的内容 = %q", got)
	}
}

func TestExtractRejectsPathTraversal(t *testing.T) {
	// zip-slip：条目名里带 ..\ 时若照单全收，就能往任意目录写文件
	zipPath := makeZip(t, map[string][]byte{
		`../../evil.exe`: []byte("EVIL"),
	})
	dst := filepath.Join(t.TempDir(), "out.exe")

	if err := extractBinary(zipPath, "evil.exe", dst); err == nil {
		t.Error("压缩包条目名含路径穿越时必须拒绝")
	}
}

func TestExtractMissingBinary(t *testing.T) {
	zipPath := makeZip(t, map[string][]byte{"readme.txt": []byte("x")})
	dst := filepath.Join(t.TempDir(), "out.exe")
	if err := extractBinary(zipPath, "ffmpeg.exe", dst); err == nil {
		t.Error("压缩包里没有目标程序时应报错")
	}
}

func TestExtractCapsEntrySize(t *testing.T) {
	// zip 炸弹：压缩率极高的条目解出来能撑爆磁盘
	big := bytes.Repeat([]byte("A"), 4<<20)
	zipPath := makeZip(t, map[string][]byte{"tool.exe": big})
	dst := filepath.Join(t.TempDir(), "out.exe")

	if err := extractBinaryLimit(zipPath, "tool.exe", dst, 1<<20); err == nil {
		t.Error("解压体积超限必须拒绝")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("超限时不应留下半个文件")
	}
}

// ---------- 安装（备份、换入、回滚）----------

func TestInstallBacksUpAndSwaps(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "yt-dlp.exe")
	staged := filepath.Join(dir, "staged.exe")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := install(staged, target); err != nil {
		t.Fatalf("install 失败: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW" {
		t.Errorf("目标文件 = %q, 期望 NEW", got)
	}
	// 换入成功后备份要清掉：ffmpeg 一份就是上百 MB，留着白占磁盘
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Error("安装成功后应清理 .old 备份")
	}
}

func TestInstallWhenTargetMissing(t *testing.T) {
	// 首次安装：目标还不存在，不该因为没得备份就失败
	dir := t.TempDir()
	target := filepath.Join(dir, "yt-dlp.exe")
	staged := filepath.Join(dir, "staged.exe")
	if err := os.WriteFile(staged, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := install(staged, target); err != nil {
		t.Fatalf("首次安装不应失败: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "NEW" {
		t.Errorf("目标文件 = %q", got)
	}
}

func TestInstallRollsBackOnFailure(t *testing.T) {
	// 换入失败时必须把原文件放回去，否则用户的内核就这么没了
	dir := t.TempDir()
	target := filepath.Join(dir, "yt-dlp.exe")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "并不存在.exe")

	if err := install(missing, target); err == nil {
		t.Fatal("换入不存在的文件应报错")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("原文件应被恢复，实际读不到: %v", err)
	}
	if string(got) != "OLD" {
		t.Errorf("回滚后内容 = %q, 期望 OLD", got)
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Error("回滚后不应留下 .old")
	}
}

// ---------- 并发与占用 ----------

func TestSingleFlight(t *testing.T) {
	// 同时更新两个内核会同时下几百 MB，把带宽和磁盘一起打满
	u := &Updater{}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		_ = u.withLock(func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	if err := u.withLock(func() error { return nil }); !errors.Is(err, ErrBusy) {
		t.Errorf("已有更新在进行时应返回 ErrBusy，实际 %v", err)
	}
	close(release)
	<-done

	// 前一个结束后应能再次进入
	if err := u.withLock(func() error { return nil }); err != nil {
		t.Errorf("上一次结束后应可再次更新，实际 %v", err)
	}
}

func TestBusyResetsAfterPanic(t *testing.T) {
	// 更新流程里如果 panic 了还占着锁，用户就再也点不动更新按钮了
	u := &Updater{}
	func() {
		defer func() { _ = recover() }()
		_ = u.withLock(func() error { panic("boom") })
	}()
	if u.Busy() {
		t.Error("panic 之后锁必须已释放")
	}
}

func TestUpdateRefusesWhenToolInUse(t *testing.T) {
	// Windows 上正在运行的 exe 换不掉；就算换得掉，半路把内核抽走也会掐断直播
	u := &Updater{InUse: func(string) bool { return true }}
	_, err := u.Update(context.Background(), Source{ToolID: "yt-dlp"}, "")
	if !errors.Is(err, ErrInUse) {
		t.Errorf("内核正被任务使用时应拒绝更新，实际 %v", err)
	}
}

func TestProxyTransport(t *testing.T) {
	got, err := proxyURL("socks5", "127.0.0.1", 1080, "u", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if got.Scheme != "socks5" || got.Host != "127.0.0.1:1080" {
		t.Errorf("代理 URL = %v", got)
	}
	if pw, _ := got.User.Password(); got.User.Username() != "u" || pw != "secret" {
		t.Errorf("代理凭据未带上: %v", got.User)
	}
	// 代理地址会随错误信息进日志，密码不能明文出现
	if strings.Contains(got.Redacted(), "secret") {
		t.Errorf("Redacted 仍暴露密码: %s", got.Redacted())
	}
}

func TestProxyRejectsUnknownType(t *testing.T) {
	if _, err := proxyURL("carrier-pigeon", "127.0.0.1", 1080, "", ""); err == nil {
		t.Error("不支持的代理类型应报错，而不是拼出一个连不上的 URL")
	}
}

func TestRedactURLHidesCredentials(t *testing.T) {
	got := redactURL("https://user:secret@example.com/a.exe")
	if strings.Contains(got, "secret") {
		t.Errorf("redactURL 仍暴露密码: %s", got)
	}
}

// ---------- 端到端（假 GitHub）----------

// fakeGitHub 起一个假的发布服务：/repos/.../releases/latest 返回发布信息，
// /dl/<name> 返回文件内容，/sums 返回校验和。
func fakeGitHub(t *testing.T, asset string, body []byte, withChecksum bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/repos/owner/name/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		assets := fmt.Sprintf(`{"name":%q,"browser_download_url":"%s/dl","size":%d}`, asset, srv.URL, len(body))
		if withChecksum {
			assets += fmt.Sprintf(`,{"name":"SHA256SUMS","browser_download_url":"%s/sums","size":100}`, srv.URL)
		}
		fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[%s]}`, assets)
	})
	mux.HandleFunc("/dl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sha256Hex(body), asset)
	})
	return srv
}

func TestUpdateEndToEndExe(t *testing.T) {
	body := []byte("新版本的可执行文件")
	srv := fakeGitHub(t, "tool.exe", body, true)

	toolsDir := filepath.Join(t.TempDir(), "tools")
	target := filepath.Join(toolsDir, "tool.exe")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("旧版本"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{Client: srv.Client(), APIBase: srv.URL}
	src := Source{ToolID: "tool", Repo: "owner/name", Asset: "tool.exe", Checksum: "SHA256SUMS", Binary: "tool.exe"}

	res, err := u.Update(context.Background(), src, toolsDir)
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if res.Release.Version != "9.9.9" {
		t.Errorf("Version = %q", res.Release.Version)
	}
	if res.BinaryPath != target {
		t.Errorf("BinaryPath = %q, 期望 %q", res.BinaryPath, target)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("目标文件未被换新: %q", got)
	}
	// 临时目录必须清干净，不能在用户的数据目录里堆下载残留
	entries, _ := os.ReadDir(toolsDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "update-") {
			t.Errorf("残留临时目录: %s", e.Name())
		}
	}
}

func TestUpdateEndToEndZip(t *testing.T) {
	// streamlink 与 ffmpeg 发的都是带一层顶层目录的 zip
	inner := []byte("FFMPEG-BINARY")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string][]byte{
		"ffmpeg-n7.1-win64-gpl/bin/ffmpeg.exe": inner,
		"ffmpeg-n7.1-win64-gpl/README.txt":     []byte("readme"),
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	srv := fakeGitHub(t, "ffmpeg-n7.1-win64-gpl.zip", buf.Bytes(), false)
	toolsDir := filepath.Join(t.TempDir(), "tools")

	u := &Updater{Client: srv.Client(), APIBase: srv.URL}
	src := Source{ToolID: "ffmpeg", Repo: "owner/name", AssetPattern: "win64-gpl.zip",
		Layout: LayoutSingleFile, Binary: "ffmpeg.exe"}

	res, err := u.Update(context.Background(), src, toolsDir)
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	got, err := os.ReadFile(res.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, inner) {
		t.Errorf("解出的内容 = %q", got)
	}
	if res.BinaryPath != filepath.Join(toolsDir, "ffmpeg.exe") {
		t.Errorf("BinaryPath = %q", res.BinaryPath)
	}
}

func TestUpdateEndToEndWholeTree(t *testing.T) {
	// streamlink 的便携包必须整包落地：只抽 exe 会丢掉它赖以运行的内嵌 Python
	body := zipBytes(t, map[string][]byte{
		"streamlink-8.5.0-1-py314-x86_64/bin/streamlink.exe": []byte("STREAMLINK"),
		"streamlink-8.5.0-1-py314-x86_64/pkgs/python313.dll": []byte("PYTHON"),
	})
	srv := fakeGitHub(t, "streamlink-8.5.0-1-py314-x86_64.zip", body, false)
	toolsDir := filepath.Join(t.TempDir(), "tools")

	u := &Updater{Client: srv.Client(), APIBase: srv.URL}
	src := Source{ToolID: "streamlink", Repo: "owner/name", AssetPattern: "-x86_64.zip",
		Layout: LayoutWholeTree, Binary: "bin/streamlink.exe"}

	res, err := u.Update(context.Background(), src, toolsDir)
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if got, _ := os.ReadFile(res.BinaryPath); string(got) != "STREAMLINK" {
		t.Errorf("可执行文件 = %q", got)
	}
	// 运行时必须一并落地，否则解出来的 exe 根本跑不起来
	runtimeDLL := filepath.Join(toolsDir, "streamlink", "pkgs", "python313.dll")
	if got, err := os.ReadFile(runtimeDLL); err != nil || string(got) != "PYTHON" {
		t.Errorf("内嵌 Python 未一并落地: %v / %q", err, got)
	}
	want := filepath.Join(toolsDir, "streamlink", "bin", "streamlink.exe")
	if res.BinaryPath != want {
		t.Errorf("BinaryPath = %q, 期望 %q", res.BinaryPath, want)
	}
}

func TestUpdateKeepsOldBinaryOnChecksumMismatch(t *testing.T) {
	// 校验和不对时绝不能动用户手上那个能用的内核
	body := []byte("被掉包的内容")
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/repos/owner/name/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"9.9.9","assets":[
          {"name":"tool.exe","browser_download_url":"%s/dl","size":%d},
          {"name":"SHA256SUMS","browser_download_url":"%s/sums","size":100}]}`, srv.URL, len(body), srv.URL)
	})
	mux.HandleFunc("/dl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  tool.exe\n", sha256Hex([]byte("本该是这个内容")))
	})

	toolsDir := t.TempDir()
	target := filepath.Join(toolsDir, "tool.exe")
	if err := os.WriteFile(target, []byte("旧版本"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{Client: srv.Client(), APIBase: srv.URL}
	src := Source{ToolID: "tool", Repo: "owner/name", Asset: "tool.exe", Checksum: "SHA256SUMS", Binary: "tool.exe"}

	if _, err := u.Update(context.Background(), src, toolsDir); err == nil {
		t.Fatal("校验和不匹配时必须失败")
	}
	if got, _ := os.ReadFile(target); string(got) != "旧版本" {
		t.Errorf("原内核被动过了: %q", got)
	}
}
