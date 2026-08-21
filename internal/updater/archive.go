package updater

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// defaultMaxTreeBytes 是整包展开的总体积上限。streamlink 便携包解出来约 200MB，
// ffmpeg 的 gpl 构建也在这个量级，留足余量的同时挡住 zip 炸弹。
const defaultMaxTreeBytes = 1 << 30

// defaultMaxEntries 是条目数上限，挡住"几十万个空文件"这类恶心包。
const defaultMaxEntries = 50000

// extractTree 把整个压缩包展开到 dst。
//
// streamlink 的便携包里除 bin/streamlink.exe 外还带着一整套内嵌 Python，
// 只抽那个 exe 出来会得到一个跑不起来的壳——所以这类内核必须整包落地。
func extractTree(zipPath, dst string) error {
	return extractTreeLimit(zipPath, dst, defaultMaxTreeBytes, defaultMaxEntries)
}

func extractTreeLimit(zipPath, dst string, maxBytes int64, maxEntries int) (err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开压缩包失败: %w", err)
	}
	defer zr.Close()

	if len(zr.File) > maxEntries {
		return fmt.Errorf("压缩包条目数 %d 超过上限 %d", len(zr.File), maxEntries)
	}

	strip := commonTopDir(zr.File)

	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	// 中途失败就把半成品清掉，不留一堆解了一半的文件让用户困惑
	defer func() {
		if err != nil {
			os.RemoveAll(dst)
		}
	}()

	var total int64
	for _, f := range zr.File {
		rel, rerr := safeRelPath(f.Name, strip)
		if rerr != nil {
			return rerr
		}
		if rel == "" {
			continue // 被剥掉的顶层目录本身
		}
		out := filepath.Join(dst, filepath.FromSlash(rel))

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(out, 0o750); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
			return err
		}
		n, werr := writeEntry(f, out, maxBytes-total)
		if werr != nil {
			return werr
		}
		total += n
	}
	return nil
}

func writeEntry(f *zip.File, dst string, remaining int64) (int64, error) {
	if remaining <= 0 {
		return 0, fmt.Errorf("解压总量超过上限")
	}
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	// 保留可执行位；zip 里没有权限信息时用 0o755，Windows 上无所谓，
	// 但这套代码也要能在别的平台跑测试
	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o755
	}
	// #nosec G304 -- dst 由 safeRelPath 校验过：已拒绝路径穿越与绝对路径，
	// 且一定落在调用方指定的解压根目录之下
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	// 多读 1 字节：读满还有得读说明确实超了
	n, err := io.Copy(out, io.LimitReader(rc, remaining+1))
	if err != nil {
		return 0, err
	}
	if n > remaining {
		return 0, fmt.Errorf("解压总量超过上限")
	}
	return n, nil
}

// safeRelPath 校验并规整压缩包条目名。
//
// 整包展开是按包内路径落盘的，zip-slip 在这里才真正有杀伤力：
// 一个 ..\..\Windows\System32\ 的条目就能往系统目录写文件。
func safeRelPath(name, strip string) (string, error) {
	clean := path.Clean(filepath.ToSlash(name))
	if strings.HasPrefix(clean, "/") || strings.Contains(clean, ":") {
		return "", fmt.Errorf("压缩包条目是绝对路径，已拒绝: %q", name)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("压缩包条目名含路径穿越，已拒绝: %q", name)
	}
	if strip != "" {
		if clean == strip {
			return "", nil
		}
		clean = strings.TrimPrefix(clean, strip+"/")
	}
	return clean, nil
}

// commonTopDir 在所有条目共处一个顶层目录时返回它，否则返回空。
//
// 这些包恒有一层带版本号的顶层目录（streamlink-8.5.0-1-py314-x86_64/）。
// 不剥掉的话，每更新一次可执行文件的路径就变一次，配置里存的路径立刻失效。
func commonTopDir(files []*zip.File) string {
	top := ""
	for _, f := range files {
		clean := path.Clean(filepath.ToSlash(f.Name))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return "" // 交给 safeRelPath 去报错
		}
		first, _, hasSlash := strings.Cut(clean, "/")
		if !hasSlash {
			return "" // 有顶层文件，说明不是"单一顶层目录"的结构
		}
		if top == "" {
			top = first
		} else if top != first {
			return ""
		}
	}
	return top
}

// installTree 把展开好的目录换成正式内核目录：旧的改名备份，新的搬进去。
//
// 必须整目录替换而不是覆盖合并：新旧两版的 Python 包混在一起，
// 会造出一堆只在特定站点才复现的怪问题。
func installTree(staged, target string) error {
	st, err := os.Stat(staged)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("暂存目录不可用: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}

	backup := target + ".old"
	_ = os.RemoveAll(backup)

	hasOld := false
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("备份原内核目录失败: %w", err)
		}
		hasOld = true
	}

	if err := os.Rename(staged, target); err != nil {
		if hasOld {
			// 回滚：把旧的搬回来，用户手上至少还有个能用的内核
			_ = os.RemoveAll(target)
			_ = os.Rename(backup, target)
		}
		return fmt.Errorf("安装新内核目录失败: %w", err)
	}
	// ffmpeg 与 streamlink 解出来都是几百 MB，备份留着白占磁盘
	_ = os.RemoveAll(backup)
	return nil
}
