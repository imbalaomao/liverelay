package monitor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Status 是一次开播探测的结论。
type Status int

const (
	// Unknown 表示没能得出结论（内核崩了、输出看不懂、网络不通）。
	// 与 Offline 分开是有意的：前者需要提示用户排查，后者是正常的等待。
	Unknown Status = iota
	Offline
	Live
)

func (s Status) String() string {
	switch s {
	case Live:
		return "已开播"
	case Offline:
		return "未开播"
	default:
		return "无法判定"
	}
}

// Result 是探测结论加一句给用户看的原因。
type Result struct {
	Status Status
	Detail string
}

// maxDetailRunes 限定写进事件日志的原因长度。内核崩溃时会吐出几百 KB 的
// traceback，原样塞进 UI 的日志列表会把内存和渲染一起拖垮（资源红线）。
const maxDetailRunes = 200

// probeJSON 是各家内核输出中我们认得的字段的并集：
// streamlink --json 给 streams / error，yt-dlp --dump-json 给 is_live。
type probeJSON struct {
	Error   string                     `json:"error"`
	Streams map[string]json.RawMessage `json:"streams"`
	IsLive  *bool                      `json:"is_live"`
}

// interpret 把一次探测的输出与退出错误翻译成结论。
//
// 退出码有意不作为判据：streamlink 未开播时以非 0 退出，
// 但那恰恰是一次成功的探测——真正的信息全在输出里。
func interpret(out []byte, execErr error) Result {
	raw := extractJSON(out)
	if raw == nil {
		return Result{Status: Unknown, Detail: unknownDetail(out, execErr)}
	}
	var p probeJSON
	if err := json.Unmarshal(raw, &p); err != nil {
		return Result{Status: Unknown, Detail: unknownDetail(out, execErr)}
	}

	switch {
	case p.Error != "":
		// "No playable streams found" 是未开播的常规信号，不是故障
		return Result{Status: Offline, Detail: clip(p.Error)}
	case p.IsLive != nil:
		if *p.IsLive {
			return Result{Status: Live, Detail: "已开播"}
		}
		return Result{Status: Offline, Detail: "源可访问，但不是直播"}
	case p.Streams != nil:
		if len(p.Streams) > 0 {
			return Result{Status: Live, Detail: fmt.Sprintf("已开播，%d 路清晰度可选", len(p.Streams))}
		}
		return Result{Status: Offline, Detail: "未开播"}
	}
	return Result{Status: Unknown, Detail: "内核返回的结果里没有可判定开播状态的字段"}
}

// extractJSON 从混杂输出里抠出 JSON 主体。
// stdout 与 stderr 是合并收的，内核的警告行会混在 JSON 前面。
func extractJSON(out []byte) []byte {
	s := string(out)
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return nil
	}
	end := strings.LastIndexByte(s, '}')
	if end < start {
		return nil
	}
	cand := []byte(s[start : end+1])
	if json.Valid(cand) {
		return cand
	}
	return nil
}

func unknownDetail(out []byte, execErr error) string {
	s := pickReason(string(out))
	switch {
	case s == "" && execErr != nil:
		return clip("探测没有任何输出：" + execErr.Error())
	case s == "":
		return "探测没有任何输出"
	case execErr != nil:
		return clip(execErr.Error() + "：" + s)
	default:
		return clip(s)
	}
}

// errorLine 匹配像是错误原因的行。
var errorLine = regexp.MustCompile(`(?i)^\s*(error|fatal|错误)\b`)

// pickReason 从多行输出里挑出最像原因的一行。
//
// 直接把整段输出截断是不行的：yt-dlp 在真正报错前会先印四行"你的版本超过 90 天了"，
// 200 字的上限刚好被这段唠叨吃满，用户看到的就只有升级提示、没有真正的错误。
func pickReason(s string) string {
	lines := strings.Split(s, "\n")
	for _, l := range lines {
		if errorLine.MatchString(l) {
			return strings.TrimSpace(l)
		}
	}
	// 没有明显的错误行就取最后一行非空内容——工具通常把结论放在最后
	// （Python traceback 的最后一行正是异常本身）
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

func clip(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) > maxDetailRunes {
		return string(r[:maxDetailRunes]) + "…"
	}
	return s
}
