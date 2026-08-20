package updater

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Layout 说明压缩包该怎么落地。
type Layout int

const (
	// LayoutSingleFile 只取出一个可执行文件。适用于静态构建（ffmpeg）。
	LayoutSingleFile Layout = iota
	// LayoutWholeTree 整包展开。适用于自带运行时的包——streamlink 的便携版
	// 除 exe 外还带着一整套内嵌 Python，只抽 exe 会得到跑不起来的壳。
	LayoutWholeTree
)

// Source 描述一个内核去哪儿找更新。
type Source struct {
	ToolID string
	// Repo 形如 owner/name，走 GitHub Releases API。
	Repo string
	// Asset 是要下载的文件名（精确匹配）。
	Asset string
	// AssetPattern 在文件名不固定时使用（含日期或构建号），按子串匹配。
	AssetPattern string
	// Checksum 是发布页里校验和文件的名字；该发布没有就跳过校验。
	Checksum string
	// Layout 决定压缩包的落地方式。
	Layout Layout
	// Binary 是可执行文件名。单文件模式下是包内要找的文件名，
	// 整包模式下是相对展开根目录的路径（如 bin/streamlink.exe）。
	Binary string
}

// builtinSources 是内置内核的更新来源。
//
// 三家的发布形态各不相同：yt-dlp 直接发 exe；streamlink 官方的 Windows 便携版
// 在独立的 windows-builds 仓库里、发的是带内嵌 Python 的 zip；
// ffmpeg 没有官方发布，用社区里最通行的 BtbN 静态构建。
var builtinSources = map[string]Source{
	"yt-dlp": {
		ToolID: "yt-dlp", Repo: "yt-dlp/yt-dlp",
		Asset: "yt-dlp.exe", Checksum: "SHA2-256SUMS",
		Layout: LayoutSingleFile, Binary: "yt-dlp.exe",
	},
	"streamlink": {
		ToolID: "streamlink", Repo: "streamlink/windows-builds",
		AssetPattern: "-x86_64.zip",
		// 整包展开：包里的 pkgs/ 是 exe 运行所必需的内嵌 Python
		Layout: LayoutWholeTree, Binary: "bin/streamlink.exe",
	},
	"ffmpeg": {
		ToolID: "ffmpeg", Repo: "BtbN/FFmpeg-Builds",
		AssetPattern: "win64-gpl.zip",
		Layout:       LayoutSingleFile, Binary: "ffmpeg.exe",
	},
}

// SourceFor 返回内置内核的更新来源。自定义内核没有来源——我们无从知道
// 用户那个二进制是打哪儿来的，猜一个只会下错东西。
func SourceFor(toolID string) (Source, bool) {
	s, ok := builtinSources[toolID]
	return s, ok
}

// Release 是一次查询得到的最新发布。
type Release struct {
	Version     string `json:"version"`
	AssetName   string `json:"assetName"`
	AssetURL    string `json:"-"`
	ChecksumURL string `json:"-"`
	Size        int64  `json:"size"`
	// Rolling 表示这个发布用的是滚动标签（BtbN/FFmpeg-Builds 恒为 latest）。
	// 这类标签没有可比较的版本号，只能让用户手动决定要不要更新。
	Rolling bool `json:"rolling"`
}

// IsArchive 判断下载下来的是压缩包还是可以直接用的可执行文件。
func (r Release) IsArchive() bool {
	return strings.HasSuffix(strings.ToLower(r.AssetName), ".zip")
}

// UpdateAvailable 判断相对当前版本是否有更新。
//
// 滚动标签一律返回 false：拿它和已装版本比永远不相等，会让用户天天看到
// "有更新"、每次白下上百 MB。宁可说"判断不了、要更新请手动点"。
func (r Release) UpdateAvailable(current string) bool {
	if r.Rolling {
		return false
	}
	return NeedsUpdate(current, r.Version)
}

type ghRelease struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// rollingTags 是各家用来表示"永远指向最新构建"的占位标签。
var rollingTags = map[string]bool{
	"latest": true, "nightly": true, "continuous": true,
	"dev": true, "master": true, "tip": true, "rolling": true,
}

func isRollingTag(tag string) bool {
	return rollingTags[strings.ToLower(strings.TrimSpace(tag))]
}

func parseRelease(body []byte, src Source) (Release, error) {
	var gr ghRelease
	if err := json.Unmarshal(body, &gr); err != nil {
		return Release{}, fmt.Errorf("解析发布信息失败: %w", err)
	}

	rel := Release{Version: normalizeVersion(gr.TagName)}
	if isRollingTag(gr.TagName) {
		rel.Rolling = true
		// 版本栏总得给用户看点有意义的东西，回落到发布日期
		if d, _, ok := strings.Cut(gr.PublishedAt, "T"); ok && d != "" {
			rel.Version = d + " 构建"
		}
	}
	for _, a := range gr.Assets {
		switch {
		case src.Asset != "" && a.Name == src.Asset,
			src.Asset == "" && src.AssetPattern != "" && strings.Contains(a.Name, src.AssetPattern):
			if rel.AssetURL == "" {
				rel.AssetName, rel.AssetURL, rel.Size = a.Name, a.URL, a.Size
			}
		case src.Checksum != "" && a.Name == src.Checksum:
			rel.ChecksumURL = a.URL
		}
	}
	if rel.AssetURL == "" {
		want := src.Asset
		if want == "" {
			want = src.AssetPattern
		}
		return Release{}, fmt.Errorf("最新发布里找不到 %q", want)
	}
	return rel, nil
}

// normalizeVersion 去掉 tag 常见的 v 前缀与两侧空白。
// 不剥掉的话，"v7.1.2" 与探测到的 "7.1.2" 会被判成不同版本，永远提示有更新。
func normalizeVersion(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 1 && (s[0] == 'v' || s[0] == 'V') && s[1] >= '0' && s[1] <= '9' {
		return s[1:]
	}
	return s
}

// NeedsUpdate 比较当前版本与最新版本。
//
// 有意不做语义化版本的大小比较：yt-dlp 是日期版本、ffmpeg 主线构建是
// N-125185-g30155f9c3a 这样的构建号，没有可靠的通用序关系。
// 只判"是不是同一个"，不同就是可更新——多提示一次远好过漏掉一次。
func NeedsUpdate(current, latest string) bool {
	latest = normalizeVersion(latest)
	if latest == "" {
		// 拿不到远端版本时不能说有更新，否则用户会白下一遍
		return false
	}
	return normalizeVersion(current) != latest
}

// findChecksum 从 SHA256SUMS 风格的文件里取出指定文件的校验和。
// 支持 "<hash>  <file>" 与 sha256sum -b 的 "<hash> *<file>" 两种写法。
func findChecksum(body []byte, name string) (string, error) {
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		got := strings.TrimPrefix(fields[len(fields)-1], "*")
		if got != name {
			continue
		}
		hash := strings.ToLower(fields[0])
		// 长度不对的一律不认，避免把某个前缀当成完整的 SHA256
		if len(hash) != 64 || !isHex(hash) {
			return "", fmt.Errorf("%q 的校验和不是合法的 SHA256: %q", name, fields[0])
		}
		return hash, nil
	}
	return "", fmt.Errorf("校验和文件里没有 %q 的条目", name)
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
