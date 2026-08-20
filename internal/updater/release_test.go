package updater

import (
	"strings"
	"testing"
)

const ytdlpReleaseJSON = `{
  "tag_name": "2026.03.17",
  "assets": [
    {"name": "yt-dlp.exe", "browser_download_url": "https://example.com/yt-dlp.exe", "size": 17825792},
    {"name": "yt-dlp_linux", "browser_download_url": "https://example.com/yt-dlp_linux", "size": 1},
    {"name": "SHA2-256SUMS", "browser_download_url": "https://example.com/SHA2-256SUMS", "size": 4096}
  ]
}`

func TestParseReleasePicksAsset(t *testing.T) {
	src := Source{ToolID: "yt-dlp", Asset: "yt-dlp.exe", Checksum: "SHA2-256SUMS"}
	rel, err := parseRelease([]byte(ytdlpReleaseJSON), src)
	if err != nil {
		t.Fatalf("parseRelease 失败: %v", err)
	}
	if rel.Version != "2026.03.17" {
		t.Errorf("Version = %q", rel.Version)
	}
	if rel.AssetURL != "https://example.com/yt-dlp.exe" {
		t.Errorf("AssetURL = %q", rel.AssetURL)
	}
	if rel.ChecksumURL != "https://example.com/SHA2-256SUMS" {
		t.Errorf("ChecksumURL = %q", rel.ChecksumURL)
	}
}

func TestParseReleaseStripsTagPrefix(t *testing.T) {
	// streamlink 的 tag 是 7.1.2，ffmpeg 构建仓库的是 latest 或 v6.1；
	// 与探测到的版本号比较时前缀必须先剥掉，否则永远判定为"有新版"
	body := `{"tag_name":"v7.1.2","assets":[{"name":"a.exe","browser_download_url":"u"}]}`
	rel, err := parseRelease([]byte(body), Source{Asset: "a.exe"})
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "7.1.2" {
		t.Errorf("Version = %q, 期望剥掉 v 前缀", rel.Version)
	}
}

func TestParseReleaseAssetPattern(t *testing.T) {
	// ffmpeg 构建的文件名带日期与构建号，只能按模式匹配
	body := `{"tag_name":"latest","assets":[
      {"name":"ffmpeg-n7.1-20260101-win64-gpl.zip","browser_download_url":"u1"},
      {"name":"ffmpeg-n7.1-20260101-linux64-gpl.tar.xz","browser_download_url":"u2"}]}`
	rel, err := parseRelease([]byte(body), Source{AssetPattern: "win64-gpl.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if rel.AssetURL != "u1" {
		t.Errorf("AssetURL = %q, 期望匹配到 win64 的那个", rel.AssetURL)
	}
}

func TestParseReleaseNoMatchingAsset(t *testing.T) {
	body := `{"tag_name":"1.0","assets":[{"name":"other.zip","browser_download_url":"u"}]}`
	if _, err := parseRelease([]byte(body), Source{Asset: "wanted.exe"}); err == nil {
		t.Error("找不到目标文件应报错，而不是悄悄下载一个别的")
	}
}

func TestParseReleaseChecksumOptional(t *testing.T) {
	// 没有校验和文件时也要能更新，只是要如实告诉用户没校验
	body := `{"tag_name":"1.0","assets":[{"name":"a.exe","browser_download_url":"u"}]}`
	rel, err := parseRelease([]byte(body), Source{Asset: "a.exe", Checksum: "SHA256SUMS"})
	if err != nil {
		t.Fatal(err)
	}
	if rel.ChecksumURL != "" {
		t.Errorf("ChecksumURL = %q, 期望空", rel.ChecksumURL)
	}
}

// ---------- 校验和 ----------

const sha256sums = `d0f2e7c1a3b4  yt-dlp_linux
e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  yt-dlp.exe
aaaa  yt-dlp.tar.gz
`

func TestFindChecksum(t *testing.T) {
	got, err := findChecksum([]byte(sha256sums), "yt-dlp.exe")
	if err != nil {
		t.Fatalf("findChecksum 失败: %v", err)
	}
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Errorf("checksum = %q, 期望 %q", got, want)
	}
}

func TestFindChecksumStarFormat(t *testing.T) {
	// sha256sum -b 生成的是 "<hash> *<file>"
	body := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 *tool.exe\n"
	if _, err := findChecksum([]byte(body), "tool.exe"); err != nil {
		t.Errorf("二进制模式的校验和行应能解析: %v", err)
	}
}

func TestFindChecksumMissing(t *testing.T) {
	if _, err := findChecksum([]byte(sha256sums), "不在里面.exe"); err == nil {
		t.Error("校验和文件里没有对应条目时必须报错——不能当作校验通过")
	}
}

func TestFindChecksumRejectsShortHash(t *testing.T) {
	// 长度不对的一律不认，避免把某个前缀当成完整的 SHA256
	if _, err := findChecksum([]byte("dead  tool.exe\n"), "tool.exe"); err == nil {
		t.Error("非 64 位十六进制不应被当成 SHA256")
	}
}

// ---------- 版本比较 ----------

func TestNeedsUpdate(t *testing.T) {
	cases := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"版本相同", "2026.03.17", "2026.03.17", false},
		{"有新版", "2026.03.17", "2026.04.01", true},
		{"当前版本未知时保守认为需要更新", "", "1.0", true},
		{"带 v 前缀视为相同", "v1.2.3", "1.2.3", false},
		{"两侧空白不影响", " 1.2.3 ", "1.2.3", false},
		// 拿不到远端版本时不能贸然说"有更新"，否则用户会白下一遍
		{"远端版本未知", "1.0", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NeedsUpdate(c.current, c.latest); got != c.want {
				t.Errorf("NeedsUpdate(%q, %q) = %v, 期望 %v", c.current, c.latest, got, c.want)
			}
		})
	}
}

// ---------- 内置来源 ----------

func TestBuiltinSources(t *testing.T) {
	for _, id := range []string{"streamlink", "yt-dlp", "ffmpeg"} {
		src, ok := SourceFor(id)
		if !ok {
			t.Fatalf("内置内核 %s 应有更新来源", id)
		}
		if src.Repo == "" || !strings.Contains(src.Repo, "/") {
			t.Errorf("%s 的 Repo = %q，应形如 owner/name", id, src.Repo)
		}
		if src.Asset == "" && src.AssetPattern == "" {
			t.Errorf("%s 未指定要下载哪个文件", id)
		}
		if src.Binary == "" {
			t.Errorf("%s 未指定最终要落地的可执行文件名", id)
		}
	}
	if _, ok := SourceFor("自定义内核"); ok {
		t.Error("自定义内核不应有内置更新来源")
	}
}

// ---------- 滚动标签 ----------

func TestRollingTagIsFlagged(t *testing.T) {
	// BtbN/FFmpeg-Builds 的 tag 恒为 "latest"。若把它当版本号比，
	// 与任何已装版本都不相等，用户会永远看到"有更新"、每次白下 100MB
	body := `{"tag_name":"latest","published_at":"2026-08-19T03:14:00Z","assets":[
	  {"name":"ffmpeg-master-latest-win64-gpl.zip","browser_download_url":"u"}]}`
	rel, err := parseRelease([]byte(body), Source{AssetPattern: "win64-gpl.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if !rel.Rolling {
		t.Error("latest 属于滚动标签，应被标记出来")
	}
	// 版本栏总得给用户看点有意义的东西
	if !strings.Contains(rel.Version, "2026-08-19") {
		t.Errorf("滚动标签应回落到发布日期，实际 %q", rel.Version)
	}
}

func TestNormalTagIsNotRolling(t *testing.T) {
	for _, tag := range []string{"2026.08.19", "v8.5.0-1", "7.1.2"} {
		body := `{"tag_name":"` + tag + `","assets":[{"name":"a.exe","browser_download_url":"u"}]}`
		rel, err := parseRelease([]byte(body), Source{Asset: "a.exe"})
		if err != nil {
			t.Fatal(err)
		}
		if rel.Rolling {
			t.Errorf("tag %q 不是滚动标签", tag)
		}
	}
}

func TestUpdateAvailableOnRollingIsUnknown(t *testing.T) {
	// 滚动构建拿不出可比较的版本号，宁可说"判断不了"也不能谎报有更新
	rel := Release{Version: "2026-08-19 构建", Rolling: true}
	if rel.UpdateAvailable("N-125185-g30155f9c3a-20260623") {
		t.Error("滚动构建不应断言有更新")
	}
	normal := Release{Version: "2026.08.19"}
	if !normal.UpdateAvailable("2026.03.17") {
		t.Error("普通标签有新版时应返回 true")
	}
	if normal.UpdateAvailable("2026.08.19") {
		t.Error("版本相同不应报有更新")
	}
}
