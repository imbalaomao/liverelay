package monitor

import (
	"errors"
	"strings"
	"testing"
)

// 真实工具的输出样本 —— 判定逻辑必须照着这些来，而不是照着想象。
const (
	streamlinkLive = `{"plugin":"twitch","metadata":{"title":"直播中"},` +
		`"streams":{"720p":{"type":"hls","url":"https://x/y.m3u8"},"best":{"type":"hls"}}}`

	streamlinkOffline = `{"error":"No playable streams found on this URL: https://example.com/live"}`

	ytdlpLive = `{"id":"abc","title":"直播","is_live":true,"formats":[{"format_id":"96"}]}`

	ytdlpVOD = `{"id":"abc","title":"录播","is_live":false,"formats":[{"format_id":"22"}]}`
)

func TestInterpretLive(t *testing.T) {
	cases := []struct {
		name string
		out  string
	}{
		{"streamlink 开播", streamlinkLive},
		{"yt-dlp 开播", ytdlpLive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := interpret([]byte(c.out), nil); got.Status != Live {
				t.Errorf("Status = %v, 期望 Live（detail=%q）", got.Status, got.Detail)
			}
		})
	}
}

func TestInterpretOffline(t *testing.T) {
	cases := []struct {
		name    string
		out     string
		execErr error
	}{
		// streamlink 未开播时以非 0 退出并打印 error 字段，两者都要能接住
		{"streamlink 未开播", streamlinkOffline, errors.New("exit status 1")},
		{"yt-dlp 是录播不是直播", ytdlpVOD, nil},
		{"streams 为空对象", `{"streams":{}}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := interpret([]byte(c.out), c.execErr); got.Status != Offline {
				t.Errorf("Status = %v, 期望 Offline（detail=%q）", got.Status, got.Detail)
			}
		})
	}
}

func TestInterpretOfflineKeepsReason(t *testing.T) {
	got := interpret([]byte(streamlinkOffline), errors.New("exit status 1"))
	if !strings.Contains(got.Detail, "No playable streams") {
		t.Errorf("Detail 应保留内核给出的原因，实际 %q", got.Detail)
	}
}

func TestInterpretIgnoresNonJSONNoise(t *testing.T) {
	// stdout 与 stderr 是合并收的，内核的警告行会混在 JSON 前面
	noisy := "[cli][warning] 无法读取配置文件\n[plugin][info] 正在解析\n" + streamlinkLive
	if got := interpret([]byte(noisy), nil); got.Status != Live {
		t.Errorf("混入警告行后 Status = %v, 期望 Live（detail=%q）", got.Status, got.Detail)
	}
}

func TestInterpretUnknown(t *testing.T) {
	cases := []struct {
		name    string
		out     string
		execErr error
	}{
		{"完全不是 JSON", "Traceback (most recent call last):\n  ...", errors.New("exit status 1")},
		{"空输出", "", errors.New("exit status 127")},
		{"JSON 但没有可判定的字段", `{"id":"abc","title":"某视频"}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := interpret([]byte(c.out), c.execErr)
			if got.Status != Unknown {
				t.Errorf("Status = %v, 期望 Unknown", got.Status)
			}
			if got.Detail == "" {
				t.Error("Unknown 必须给出原因，否则用户面对一个不动的任务无从排查")
			}
		})
	}
}

func TestInterpretDetailIsBounded(t *testing.T) {
	// 内核可能吐出几百 KB 的 traceback，原样塞进事件日志会把 UI 拖垮
	huge := strings.Repeat("错误", 100000)
	got := interpret([]byte(huge), errors.New("exit status 1"))
	if len([]rune(got.Detail)) > maxDetailRunes+8 {
		t.Errorf("Detail 长度 %d 未被截断", len([]rune(got.Detail)))
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"纯 JSON", `{"a":1}`, true},
		{"前置噪声", "warning: x\n" + `{"a":1}`, true},
		{"后置噪声", `{"a":1}` + "\ndone", true},
		{"没有大括号", "plain text", false},
		{"括号不成对", `{"a":1`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractJSON([]byte(c.in))
			if (got != nil) != c.want {
				t.Errorf("extractJSON(%q) = %q", c.in, got)
			}
		})
	}
}

// ytdlpNoisyError 是 yt-dlp 对一个打不开的 URL 的真实输出：
// 真正的原因被四行"版本过期"提醒挡在后面。
const ytdlpNoisyError = `WARNING: Your yt-dlp version (2026.03.17) is older than 90 days!
         It is strongly recommended to always use the latest version.
         Run "yt-dlp --update" or "yt-dlp -U" to update.
         To suppress this warning, add --no-update to your command/config.
ERROR: [generic] Unable to download webpage: HTTP Error 404: Not Found (caused by <HTTPError 404: Not Found>)`

func TestInterpretPicksErrorLineOverLeadingNoise(t *testing.T) {
	got := interpret([]byte(ytdlpNoisyError), nil)
	if got.Status != Unknown {
		t.Errorf("Status = %v, 期望 Unknown", got.Status)
	}
	if !strings.Contains(got.Detail, "404") {
		t.Errorf("Detail 应给出真正的错误而不是版本提醒，实际 %q", got.Detail)
	}
	if strings.Contains(got.Detail, "older than 90 days") {
		t.Errorf("Detail 不该被无关的升级提醒占满，实际 %q", got.Detail)
	}
}

func TestPickReasonFallsBackToLastLine(t *testing.T) {
	// Python traceback 没有 ERROR 行，但最后一行正是异常本身
	tb := "Traceback (most recent call last):\n  File \"x.py\", line 1\n    foo()\nValueError: 无效的 URL"
	if got := pickReason(tb); !strings.Contains(got, "ValueError") {
		t.Errorf("pickReason = %q，期望取到最后一行的异常", got)
	}
}
