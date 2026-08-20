// Package weibo 对接微博直播开播接口：凭用户的登录 cookie 换取推流地址与观看地址。
package weibo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	// ErrExpired 表示微博明确拒绝了这份 cookie，需要重新登录录入。
	ErrExpired = errors.New("微博 cookie 已失效，请重新录入")
	// ErrNoCookie 表示还没录入 cookie。
	ErrNoCookie = errors.New("尚未录入微博 cookie")
)

// configURL 是微博直播控制台读取推流配置的接口。
const configURL = "https://weibo.com"

const configPath = "/l/pc/config/index"

// referer 必须是直播创建页；缺了它微博会拒绝这个请求。
const referer = "https://weibo.com/l/wblive/admin/home/livecreate?stream_type=0"

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

// defaultMaxBody 是响应体上限。正常返回只有几 KB，
// 封顶是为了防着对端（或中间的网关）无限吐数据把内存吃光。
const defaultMaxBody = 1 << 20

const defaultTimeout = 20 * time.Second

// StreamInfo 是一次成功查询拿到的开播信息。
type StreamInfo struct {
	// PushURL 与 PushKey 是分开的：推流码要单独脱敏，不能混在地址里进日志。
	PushURL string `json:"pushUrl"`
	PushKey string `json:"-"`
	PushSRT string `json:"-"`

	WatchHLS  string `json:"watchHls"`
	WatchFLV  string `json:"watchFlv"`
	WatchRTMP string `json:"watchRtmp"`
}

// Client 调用微博接口。零值可用。
type Client struct {
	HTTP    *http.Client
	BaseURL string
	// MaxBodyBytes 覆盖响应体上限，测试用。
	MaxBodyBytes int64
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: defaultTimeout}
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return configURL
}

func (c *Client) maxBody() int64 {
	if c.MaxBodyBytes > 0 {
		return c.MaxBodyBytes
	}
	return defaultMaxBody
}

// 微博返回结构里我们认得的部分。
type configResponse struct {
	Data struct {
		Item struct {
			StreamInfo *struct {
				PushRTMP    string `json:"push_rtmp"`
				PushAuthURL string `json:"push_auth_url"`
				PushAuthKey string `json:"push_auth_key"`
				PushSRT     string `json:"push_srt"`
				PullFree    *pull  `json:"pull_free"`
				Pull        *pull  `json:"pull"`
			} `json:"stream_info"`
		} `json:"item"`
	} `json:"data"`
}

type pull struct {
	FLV  string `json:"live_origin_flv_url"`
	HLS  string `json:"live_origin_hls_url"`
	RTMP string `json:"live_origin_rtmp_url"`
}

// Fetch 用给定 cookie 查询开播信息。
//
// 错误分三类，调用方必须区别对待：
//   - ErrNoCookie：还没录入
//   - ErrExpired：微博明确拒绝了这份 cookie，需要重新登录
//   - 其它：网络不通、网关拦截、微博抽风等。这类**不能**当成 cookie 过期，
//     否则用户断一次网就被要求重新登录一遍。
func (c *Client) Fetch(ctx context.Context, cookie string) (StreamInfo, error) {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return StreamInfo{}, ErrNoCookie
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+configPath, nil)
	if err != nil {
		return StreamInfo{}, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", referer)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cookie", cookie)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		// 有意不带上 err 之外的任何上下文：请求头里有 cookie，
		// 拼进错误信息就等于把账号凭据写进日志
		return StreamInfo{}, fmt.Errorf("请求微博接口失败: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return StreamInfo{}, ErrExpired
	case resp.StatusCode != http.StatusOK:
		return StreamInfo{}, fmt.Errorf("微博接口返回 %s", resp.Status)
	}

	// 多读 1 字节：读满上限还有得读，说明确实超了
	limit := c.maxBody()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return StreamInfo{}, fmt.Errorf("读取微博响应失败: %w", err)
	}
	if int64(len(body)) > limit {
		return StreamInfo{}, fmt.Errorf("微博响应超过 %d 字节上限", limit)
	}

	var cr configResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		// 返回的不是 JSON，多半是被网关或登录页拦了，请求根本没到直播接口。
		// 这不能算 cookie 过期。
		return StreamInfo{}, errors.New("微博返回的不是预期的 JSON，可能被网关拦截或需要重新登录网页版")
	}

	si := cr.Data.Item.StreamInfo
	if si == nil {
		// HTTP 200 却拿不到 stream_info —— 这才是 cookie 过期的确切信号
		return StreamInfo{}, ErrExpired
	}

	out := StreamInfo{
		PushURL: strings.TrimSpace(si.PushAuthURL),
		PushKey: strings.TrimSpace(si.PushAuthKey),
		PushSRT: strings.TrimSpace(si.PushSRT),
	}
	if out.PushURL == "" || out.PushKey == "" {
		// 只给了合并式地址时自己拆开
		out.PushURL, out.PushKey = splitPushURL(si.PushRTMP)
	}
	if out.PushURL == "" || out.PushKey == "" {
		return StreamInfo{}, errors.New("微博没有返回可用的推流地址，请确认账号已开通直播权限")
	}

	p := si.PullFree
	if p == nil {
		p = si.Pull
	}
	if p != nil {
		out.WatchHLS = cleanWatchURL(p.HLS)
		out.WatchFLV = cleanWatchURL(p.FLV)
		out.WatchRTMP = cleanRTMPURL(p.RTMP)
	}
	return out, nil
}
