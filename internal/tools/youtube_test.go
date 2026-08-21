package tools

import (
	"strings"
	"testing"

	"github.com/imbalaomao/liverelay/internal/config"
)

func TestIsYouTubeURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://youtube.com/live/xyz", true},
		{"https://m.youtube.com/watch?v=abc", true},
		{"https://music.youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc", true},
		{"http://www.youtube.com/@someone/live", true},
		{"HTTPS://WWW.YOUTUBE.COM/watch?v=abc", true},
		{"www.youtube.com/watch?v=abc", true},
		// 别的站点不能误伤
		{"https://www.twitch.tv/someone", false},
		{"https://live.bilibili.com/123", false},
		{"", false},
		// 域名里含 youtube 字样但不是 youtube 的站，不能算
		{"https://notyoutube.com/watch", false},
		{"https://youtube.com.evil.example/watch", false},
		// 路径或查询串里出现 youtube 也不算
		{"https://example.com/?ref=youtube.com", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := IsYouTubeURL(c.in); got != c.want {
				t.Errorf("IsYouTubeURL(%q) = %v, 期望 %v", c.in, got, c.want)
			}
		})
	}
}

func TestNeedsYouTubeCookies(t *testing.T) {
	ytdlp := config.Tool{ID: "yt-dlp", Name: "yt-dlp"}
	sl := config.Tool{ID: "streamlink", Name: "streamlink"}
	yt := "https://www.youtube.com/watch?v=abc"
	tw := "https://www.twitch.tv/x"

	cases := []struct {
		name string
		tool config.Tool
		url  string
		want bool
	}{
		{"yt-dlp 抓 YouTube，需要 cookie", ytdlp, yt, true},
		{"yt-dlp 抓别的站，不需要", ytdlp, tw, false},
		// streamlink 走的是另一套解析，不吃这套人机验证
		{"streamlink 抓 YouTube，不适用", sl, yt, false},
		{"两者都不沾", sl, tw, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NeedsYouTubeCookies(c.tool, c.url, ""); got != c.want {
				t.Errorf("= %v, 期望 %v", got, c.want)
			}
		})
	}
}

func TestNeedsYouTubeCookiesSatisfiedByCookieFile(t *testing.T) {
	// 已经配了 cookie 文件就不该再拦
	ytdlp := config.Tool{ID: "yt-dlp"}
	yt := "https://youtu.be/abc"
	if NeedsYouTubeCookies(ytdlp, yt, `D:\cookies.txt`) {
		t.Error("已提供 cookie 文件时不应再拦截")
	}
	if !NeedsYouTubeCookies(ytdlp, yt, "   ") {
		t.Error("空白路径等同没配")
	}
}

func TestNeedsYouTubeCookiesMatchesCustomYtdlpBuilds(t *testing.T) {
	// 用户可能把 yt-dlp 的自定义构建注册成别的 ID，
	// 按名字或路径识别，别让改个 ID 就绕过了提示
	for _, tool := range []config.Tool{
		{ID: "yt-dlp-nightly", Name: "yt-dlp nightly"},
		config.Tool{ID: "my-fetcher", Name: "yt-dlp（自编译）"},
		config.Tool{ID: "custom", Name: "自定义", Path: `D:\bin\yt-dlp_x86.exe`},
	} {
		if !NeedsYouTubeCookies(tool, "https://youtu.be/x", "") {
			t.Errorf("%+v 应被识别为 yt-dlp 系", tool)
		}
	}
	// 但不能误伤真正无关的内核
	if NeedsYouTubeCookies(config.Tool{ID: "n-m3u8dl-re", Name: "N_m3u8DL-RE"}, "https://youtu.be/x", "") {
		t.Error("无关内核不应被识别为 yt-dlp 系")
	}
}

func TestYouTubeCookieHintIsActionable(t *testing.T) {
	msg := YouTubeCookieHint()
	for _, want := range []string{"YouTube", "Cookie", "Netscape"} {
		if !strings.Contains(msg, want) {
			t.Errorf("提示应说明要做什么，缺少 %q：%s", want, msg)
		}
	}
}
