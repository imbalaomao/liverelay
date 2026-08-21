package tools

import (
	"net/url"
	"strings"

	"github.com/imbalaomao/liverelay/internal/config"
)

// youtubeHosts 是 YouTube 的域名。必须整段比对而不是子串包含：
// notyoutube.com、youtube.com.evil.example 都含有 "youtube.com"。
var youtubeHosts = map[string]bool{
	"youtube.com": true,
	"youtu.be":    true,
}

// IsYouTubeURL 判断一个地址是不是 YouTube。
func IsYouTubeURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	// 用户常常只贴 www.youtube.com/... 而不带协议头，补上才能正常解析
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if youtubeHosts[host] {
		return true
	}
	// 允许 www./m./music. 这类子域，但要求父域正好是 YouTube
	if i := strings.IndexByte(host, '.'); i >= 0 {
		return youtubeHosts[host[i+1:]]
	}
	return false
}

// ytdlpMarkers 用来认出 yt-dlp 系的内核。
// 光比 ID 不够：用户可能把自编译的 yt-dlp 注册成任意 ID，
// 改个名字就绕过提示、到开播时才撞上人机验证，那还不如不提示。
var ytdlpMarkers = []string{"yt-dlp", "ytdlp", "yt_dlp", "youtube-dl"}

// IsYtdlpFamily 判断内核是不是 yt-dlp 系。
func IsYtdlpFamily(t config.Tool) bool {
	blob := strings.ToLower(t.ID + " " + t.Name + " " + t.Path + " " + t.PathOverride)
	for _, m := range ytdlpMarkers {
		if strings.Contains(blob, m) {
			return true
		}
	}
	return false
}

// NeedsYouTubeCookies 报告这次抓流是否会撞上 YouTube 的人机验证。
//
// yt-dlp 拉 YouTube 时大概率被要求验证「我不是机器人」，没有登录 cookie 就取不到流。
// 与其让任务启动后卡在一个看不懂的报错上，不如在开播前就拦下来把话说清楚。
// streamlink 走的是另一套解析，不适用此限制。
func NeedsYouTubeCookies(t config.Tool, sourceURL, cookieFile string) bool {
	if strings.TrimSpace(cookieFile) != "" {
		return false
	}
	return IsYtdlpFamily(t) && IsYouTubeURL(sourceURL)
}

// YouTubeCookieHint 是拦下来时给用户看的说明。
func YouTubeCookieHint() string {
	return "yt-dlp 抓取 YouTube 时通常会被要求人机验证，需要先提供 YouTube Cookies：" +
		"用浏览器扩展（如 Get cookies.txt）把已登录的 youtube.com 站点 Cookie 导出为 " +
		"Netscape 格式的 cookies.txt，再到「设置」里指定该文件。"
}
