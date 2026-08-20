package weibo

import (
	"regexp"
	"strings"
)

// transcodeSuffix 匹配微博给观看地址加的转码档位后缀（_wb720avc / _wb1080avc）。
// 去掉它拿到的才是原画流。档位数字位数不定，不能只认 720。
var transcodeSuffix = regexp.MustCompile(`_wb\d+avc\.(flv|m3u8)$`)

// transcodeSuffixRTMP 是 rtmp 观看地址的版本——它没有扩展名。
var transcodeSuffixRTMP = regexp.MustCompile(`_wb\d+avc$`)

// freeDomainHTTP 匹配免流域名前缀。微博把真实域名整个当路径拼在 free.sinaimg.cn
// 后面，这个域名只对特定运营商可用，原样给用户会在别的网络下打不开。
var freeDomainHTTP = regexp.MustCompile(`^https?://free\.sinaimg\.cn/`)

var freeDomainRTMP = regexp.MustCompile(`^rtmp://free\.sinaimg\.cn/`)

// cleanWatchURL 把微博给的 HTTP 观看地址收拾成可以直接分享的形式。
func cleanWatchURL(u string) string {
	if u == "" {
		return ""
	}
	u = transcodeSuffix.ReplaceAllString(u, ".$1")
	return freeDomainHTTP.ReplaceAllString(u, "https://")
}

// cleanRTMPURL 是 rtmp 观看地址的版本。
func cleanRTMPURL(u string) string {
	if u == "" {
		return ""
	}
	u = transcodeSuffixRTMP.ReplaceAllString(u, "")
	return freeDomainRTMP.ReplaceAllString(u, "rtmp://")
}

// splitPushURL 把合并式的推流地址拆成"地址 + 推流码"。
//
// 微博多数情况下会直接给 push_auth_url / push_auth_key，但有些账号只给一条
// 完整的 push_rtmp。推流工具普遍要求两者分开填，而且我们的 Target 结构也是
// 分开的——推流码要单独脱敏，不能混在地址里进日志。
func splitPushURL(full string) (url, key string) {
	full = strings.TrimSpace(full)
	if full == "" {
		return "", ""
	}
	i := strings.LastIndex(full, "/")
	if i < 0 || i+1 >= len(full) {
		return full, ""
	}
	return full[:i+1], full[i+1:]
}
