// Package updater 负责检查并下载内置内核（streamlink / yt-dlp / ffmpeg）的更新。
package updater

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrBusy  = errors.New("已有更新正在进行")
	ErrInUse = errors.New("内核正被任务使用")
)

// defaultMaxDownload 是单次下载的硬上限。ffmpeg 的完整构建约 150MB，
// 留出余量的同时挡住"服务端无限吐数据把磁盘撑满"。
const defaultMaxDownload = 512 << 20

// defaultMaxExtract 是单个解压条目的上限，用来挡 zip 炸弹。
const defaultMaxExtract = 512 << 20

// httpTimeout 覆盖整个下载过程。大文件 + 慢代理，给足时间。
const httpTimeout = 30 * time.Minute

// Progress 是下载进度回调的入参。
type Progress struct {
	Downloaded int64
	Total      int64
}

// Updater 执行检查与更新。零值可用。
type Updater struct {
	Client           *http.Client
	MaxDownloadBytes int64
	// InUse 用来问外部"这个内核正被任务占着吗"。Windows 上运行中的 exe
	// 换不掉；就算换得掉，半路把内核抽走也会掐断正在进行的直播。
	InUse func(toolID string) bool
	// OnProgress 可选，用于把下载进度推给 UI。
	OnProgress func(toolID string, p Progress)
	// APIBase 默认指向 GitHub；测试用假服务替换。
	APIBase string

	mu   sync.Mutex
	busy bool
}

// NewClient 按代理设置构造 HTTP 客户端。
func NewClient(enabled bool, typ, host string, port int, user, pass string) (*http.Client, error) {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
	}
	if enabled && host != "" {
		u, err := proxyURL(typ, host, port, user, pass)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: tr, Timeout: httpTimeout}, nil
}

func proxyURL(typ, host string, port int, user, pass string) (*url.URL, error) {
	scheme := strings.ToLower(strings.TrimSpace(typ))
	switch scheme {
	case "", "http":
		scheme = "http"
	case "https", "socks5":
	default:
		return nil, fmt.Errorf("不支持的代理类型 %q", typ)
	}
	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port))}
	if user != "" {
		u.User = url.UserPassword(user, pass)
	}
	return u, nil
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: httpTimeout}
}

func (u *Updater) maxDownload() int64 {
	if u.MaxDownloadBytes > 0 {
		return u.MaxDownloadBytes
	}
	return defaultMaxDownload
}

// Busy 报告是否有更新正在进行。
func (u *Updater) Busy() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.busy
}

// withLock 保证同一时刻只有一个更新在跑：同时更新多个内核会一起下几百 MB，
// 把带宽和磁盘一并打满。defer 释放，中途 panic 也不会把锁永久占死。
func (u *Updater) withLock(fn func() error) error {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return ErrBusy
	}
	u.busy = true
	u.mu.Unlock()

	defer func() {
		u.mu.Lock()
		u.busy = false
		u.mu.Unlock()
	}()
	return fn()
}

// Check 查询某个内核的最新发布。
func (u *Updater) Check(ctx context.Context, src Source) (Release, error) {
	base := u.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	api := strings.TrimSuffix(base, "/") + "/repos/" + src.Repo + "/releases/latest"
	body, err := u.get(ctx, api, 4<<20)
	if err != nil {
		return Release{}, err
	}
	return parseRelease(body, src)
}

// Result 是一次成功更新的落地结果。
type Result struct {
	Release Release `json:"release"`
	// BinaryPath 是更新后可执行文件的实际位置，应当写回 Tool.Path。
	// 整包模式下这个路径与更新前不同，不写回就会继续用着旧目录。
	BinaryPath string `json:"binaryPath"`
}

// Update 下载并安装某个内核。toolsDir 是内核存放目录（通常是 <数据根>/tools）。
func (u *Updater) Update(ctx context.Context, src Source, toolsDir string) (Result, error) {
	if u.InUse != nil && u.InUse(src.ToolID) {
		return Result{}, fmt.Errorf("%w：请先停止相关任务再更新", ErrInUse)
	}
	var res Result
	err := u.withLock(func() error {
		var err error
		res, err = u.doUpdate(ctx, src, toolsDir)
		return err
	})
	return res, err
}

func (u *Updater) doUpdate(ctx context.Context, src Source, toolsDir string) (Result, error) {
	rel, err := u.Check(ctx, src)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(toolsDir, 0o750); err != nil {
		return Result{}, err
	}

	tmp, err := os.MkdirTemp(toolsDir, "update-*")
	if err != nil {
		return Result{}, fmt.Errorf("创建临时目录失败: %w", err)
	}
	// 无论成败都清干净，别在用户的数据目录里堆下载残留
	defer os.RemoveAll(tmp)

	var want *string
	if rel.ChecksumURL != "" {
		body, cerr := u.get(ctx, rel.ChecksumURL, 4<<20)
		if cerr != nil {
			return Result{}, cerr
		}
		sum, cerr := findChecksum(body, rel.AssetName)
		if cerr != nil {
			return Result{}, cerr
		}
		want = &sum
	}

	staged := filepath.Join(tmp, rel.AssetName)
	if err := u.downloadWithProgress(ctx, src.ToolID, rel, staged, want); err != nil {
		return Result{}, err
	}

	switch {
	case rel.IsArchive() && src.Layout == LayoutWholeTree:
		return u.installArchiveTree(src, rel, staged, tmp, toolsDir)
	case rel.IsArchive():
		exe := filepath.Join(tmp, path.Base(src.Binary))
		if err := extractBinary(staged, path.Base(src.Binary), exe); err != nil {
			return Result{}, err
		}
		staged = exe
	}

	target := filepath.Join(toolsDir, path.Base(src.Binary))
	if err := install(staged, target); err != nil {
		return Result{}, err
	}
	return Result{Release: rel, BinaryPath: target}, nil
}

// installArchiveTree 处理"整包落地"的内核：展开到临时目录，再整目录换入。
func (u *Updater) installArchiveTree(src Source, rel Release, archive, tmp, toolsDir string) (Result, error) {
	unpacked := filepath.Join(tmp, "unpacked")
	if err := extractTree(archive, unpacked); err != nil {
		return Result{}, err
	}
	// 换入之前先确认包里确实有那个可执行文件，免得把好好的旧目录换成一堆废文件
	if _, err := os.Stat(filepath.Join(unpacked, filepath.FromSlash(src.Binary))); err != nil {
		return Result{}, fmt.Errorf("压缩包里没有 %q", src.Binary)
	}

	targetDir := filepath.Join(toolsDir, src.ToolID)
	if err := installTree(unpacked, targetDir); err != nil {
		return Result{}, err
	}
	return Result{
		Release:    rel,
		BinaryPath: filepath.Join(targetDir, filepath.FromSlash(src.Binary)),
	}, nil
}

func (u *Updater) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "LiveRelay")

	resp, err := u.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 %s 失败: %w", redactURL(rawURL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("请求 %s 返回 %s", redactURL(rawURL), resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (u *Updater) downloadWithProgress(ctx context.Context, toolID string, rel Release, dst string, wantSHA *string) error {
	if u.OnProgress == nil {
		return u.download(ctx, rel.AssetURL, dst, wantSHA)
	}
	// 进度回调只是包一层，真正的落盘逻辑仍在 download 里
	u.OnProgress(toolID, Progress{Downloaded: 0, Total: rel.Size})
	err := u.download(ctx, rel.AssetURL, dst, wantSHA)
	if err == nil {
		u.OnProgress(toolID, Progress{Downloaded: rel.Size, Total: rel.Size})
	}
	return err
}

// download 把 URL 的内容流式写到 dst，并在 wantSHA 非空时校验。
//
// 全程流式：内核动辄上百 MB，读进内存再落盘会直接顶穿内存红线。
// 任何一步失败都把半成品删掉——留在盘上就有被当成内核执行的风险。
func (u *Updater) download(ctx context.Context, rawURL, dst string, wantSHA *string) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "LiveRelay")

	resp, err := u.client().Do(req)
	if err != nil {
		return fmt.Errorf("下载 %s 失败: %w", redactURL(rawURL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 %s 返回 %s", redactURL(rawURL), resp.Status)
	}

	// #nosec G304 -- dst 由本函数的调用方用 os.MkdirTemp 建的临时目录拼出，
	// 不含任何外部可控片段
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(dst)
		}
	}()

	limit := u.maxDownload()
	h := sha256.New()
	// 多读 1 字节：读满上限还有得读，说明确实超了
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("写入下载内容失败: %w", err)
	}
	if n > limit {
		return fmt.Errorf("下载体积超过上限 %d 字节", limit)
	}
	if err = f.Sync(); err != nil {
		return err
	}

	if wantSHA != nil {
		got := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(got, *wantSHA) {
			err = fmt.Errorf("校验和不匹配：期望 %s，实际 %s", *wantSHA, got)
			return err
		}
	}
	return nil
}

// extractBinary 从压缩包里取出指定的可执行文件。
func extractBinary(zipPath, binary, dst string) error {
	return extractBinaryLimit(zipPath, binary, dst, defaultMaxExtract)
}

func extractBinaryLimit(zipPath, binary, dst string, limit int64) (err error) {
	// #nosec G304 -- zipPath 是我们刚下载到临时目录的文件
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开压缩包失败: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		// zip-slip：条目名可以是 ../../evil.exe。我们只按文件名取用、
		// 从不按包内路径落盘，但仍要挡住这种包——它本身就是恶意信号。
		if strings.Contains(f.Name, "..") {
			return fmt.Errorf("压缩包条目名含路径穿越，已拒绝: %q", f.Name)
		}
		if path.Base(filepath.ToSlash(f.Name)) != binary {
			continue
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return oerr
		}
		defer rc.Close()

		// #nosec G304 -- dst 由调用方用 os.MkdirTemp 建的临时目录拼出
		out, cerr := os.Create(dst)
		if cerr != nil {
			return cerr
		}
		defer func() {
			out.Close()
			if err != nil {
				os.Remove(dst)
			}
		}()

		n, cerr := io.Copy(out, io.LimitReader(rc, limit+1))
		if cerr != nil {
			err = cerr
			return err
		}
		if n > limit {
			err = fmt.Errorf("解压体积超过上限 %d 字节", limit)
			return err
		}
		return nil
	}
	return fmt.Errorf("压缩包里没有 %q", binary)
}

// install 把暂存文件换成正式内核：先把旧的改名备份，再把新的搬进去。
//
// 用改名而非覆盖，是因为 Windows 上运行中的 exe 可以被改名却不能被覆盖；
// 任何一步失败都要把旧的放回原位，绝不能让用户的内核凭空消失。
func install(staged, target string) error {
	if _, err := os.Stat(staged); err != nil {
		return fmt.Errorf("暂存文件不可用: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}

	backup := target + ".old"
	_ = os.Remove(backup) // 上一次留下的残留

	hasOld := false
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("备份原内核失败: %w", err)
		}
		hasOld = true
	}

	if err := moveFile(staged, target); err != nil {
		if hasOld {
			// 回滚：把旧的搬回来，用户手上至少还有个能用的内核
			_ = os.Remove(target)
			_ = os.Rename(backup, target)
		}
		return fmt.Errorf("安装新内核失败: %w", err)
	}
	// 换入成功，备份就可以清了——ffmpeg 一份上百 MB，留着白占磁盘
	_ = os.Remove(backup)
	return nil
}

// moveFile 先试改名；跨卷时退回复制。
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// #nosec G304 -- src 是本进程刚落地的暂存文件
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// #nosec G302 G304 -- 换入的是可执行文件，0600 不带执行位就跑不起来，
	// 0700 已是"仅本用户可执行"的最小权限；dst 由调用方在内核目录下拼出，
	// 不含外部可控片段
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// redactURL 抹掉 URL 里的凭据，避免代理密码进日志。
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Redacted()
}
