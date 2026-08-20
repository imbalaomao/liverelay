package weibo

import (
	"testing"
)

// ---------- 观看地址清洗 ----------

func TestCleanWatchURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// 微博给的是转码流，_wb720avc 是 720p 档；去掉后缀才是原画
			"去掉转码后缀 m3u8",
			"https://f.us.sinaimg.cn/live/1234_wb720avc.m3u8",
			"https://f.us.sinaimg.cn/live/1234.m3u8",
		},
		{
			"去掉转码后缀 flv",
			"https://f.us.sinaimg.cn/live/1234_wb1080avc.flv",
			"https://f.us.sinaimg.cn/live/1234.flv",
		},
		{
			// free.sinaimg.cn 是免流域名，它把真实域名整个当路径前缀套在后面，
			// 只对特定运营商可用，直接给用户会在别的网络下打不开
			"剥掉免流域名前缀，露出真实域名",
			"https://free.sinaimg.cn/f.us.sinaimg.cn/live/1234.m3u8",
			"https://f.us.sinaimg.cn/live/1234.m3u8",
		},
		{
			"两种都要处理",
			"http://free.sinaimg.cn/f.us.sinaimg.cn/live/5678_wb720avc.m3u8",
			"https://f.us.sinaimg.cn/live/5678.m3u8",
		},
		{"本来就干净的地址原样返回", "https://f.us.sinaimg.cn/live/1234.m3u8", "https://f.us.sinaimg.cn/live/1234.m3u8"},
		{"空串", "", ""},
		// 数字位数不定，不能只认 720
		{"任意档位", "https://x/y_wb480avc.m3u8", "https://x/y.m3u8"},
		// 后缀出现在中间不是转码标记，不能动
		{"中间出现的同名片段不处理", "https://x/_wb720avc_y.m3u8", "https://x/_wb720avc_y.m3u8"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cleanWatchURL(c.in); got != c.want {
				t.Errorf("cleanWatchURL(%q) = %q, 期望 %q", c.in, got, c.want)
			}
		})
	}
}

func TestCleanRTMPWatchURL(t *testing.T) {
	// rtmp 观看地址没有扩展名，后缀规则不一样
	cases := []struct{ in, want string }{
		{"rtmp://f.us.sinaimg.cn/live/1234_wb720avc", "rtmp://f.us.sinaimg.cn/live/1234"},
		{"rtmp://free.sinaimg.cn/rtmp.us.sinaimg.cn/live/1234", "rtmp://rtmp.us.sinaimg.cn/live/1234"},
		{"", ""},
	}
	for _, c := range cases {
		if got := cleanRTMPURL(c.in); got != c.want {
			t.Errorf("cleanRTMPURL(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
