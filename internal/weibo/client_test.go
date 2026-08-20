package weibo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// realishResponse 仿微博 /l/pc/config/index 的真实返回形态。
const realishResponse = `{
  "code": "100000",
  "data": {
    "item": {
      "stream_info": {
        "push_rtmp": "rtmp://push.alive.sinaimg.cn/alive/abc123?auth_key=KEY",
        "push_auth_url": "rtmp://push.alive.sinaimg.cn/alive/",
        "push_auth_key": "abc123?auth_key=KEY",
        "push_srt": "srt://push.alive.sinaimg.cn:9000?streamid=abc123",
        "pull_free": {
          "live_origin_flv_url": "https://free.sinaimg.cn/f.us.sinaimg.cn/alive/abc123_wb720avc.flv",
          "live_origin_hls_url": "https://free.sinaimg.cn/f.us.sinaimg.cn/alive/abc123_wb720avc.m3u8",
          "live_origin_rtmp_url": "rtmp://free.sinaimg.cn/rtmp.us.sinaimg.cn/alive/abc123_wb720avc"
        }
      }
    }
  }
}`

// notLoggedIn 是 cookie 失效时的返回：HTTP 200，但结构里没有 stream_info。
const notLoggedIn = `{"code":"100006","msg":"未登录","data":{}}`

func testServer(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), BaseURL: srv.URL}, srv
}

// ---------- 成功路径 ----------

func TestFetchParsesStreamInfo(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(realishResponse))
	})

	info, err := c.Fetch(context.Background(), "SUB=abc; SUBP=def")
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	// 分离式的地址与推流码正好对上我们的 Target 结构
	if info.PushURL != "rtmp://push.alive.sinaimg.cn/alive/" {
		t.Errorf("PushURL = %q", info.PushURL)
	}
	if info.PushKey != "abc123?auth_key=KEY" {
		t.Errorf("PushKey = %q", info.PushKey)
	}
	if info.PushSRT == "" {
		t.Error("PushSRT 为空")
	}
	// 观看地址必须已清洗：去转码后缀 + 剥免流域名
	if info.WatchHLS != "https://f.us.sinaimg.cn/alive/abc123.m3u8" {
		t.Errorf("WatchHLS = %q", info.WatchHLS)
	}
	if info.WatchFLV != "https://f.us.sinaimg.cn/alive/abc123.flv" {
		t.Errorf("WatchFLV = %q", info.WatchFLV)
	}
}

func TestFetchFallsBackToCombinedPushURL(t *testing.T) {
	// 有些账号只给 push_rtmp 合并式地址，得能拆出地址与推流码
	body := `{"data":{"item":{"stream_info":{
	  "push_rtmp":"rtmp://push.alive.sinaimg.cn/alive/abc123?auth_key=KEY",
	  "pull_free":{"live_origin_hls_url":"https://x/y.m3u8"}}}}}`
	c, _ := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	info, err := c.Fetch(context.Background(), "ck")
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if info.PushURL != "rtmp://push.alive.sinaimg.cn/alive/" {
		t.Errorf("PushURL = %q", info.PushURL)
	}
	if info.PushKey != "abc123?auth_key=KEY" {
		t.Errorf("PushKey = %q", info.PushKey)
	}
}

func TestFetchUsesPullWhenPullFreeMissing(t *testing.T) {
	body := `{"data":{"item":{"stream_info":{
	  "push_rtmp":"rtmp://h/app/key",
	  "pull":{"live_origin_hls_url":"https://x/y_wb720avc.m3u8"}}}}}`
	c, _ := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	info, err := c.Fetch(context.Background(), "ck")
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if info.WatchHLS != "https://x/y.m3u8" {
		t.Errorf("WatchHLS = %q", info.WatchHLS)
	}
}

// ---------- 请求头 ----------

func TestFetchSendsRequiredHeaders(t *testing.T) {
	// 少了 Referer 微博会拒绝；Cookie 自然也不能少
	var got http.Header
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(realishResponse))
	})

	if _, err := c.Fetch(context.Background(), "SUB=abc"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Get("Referer"), "livecreate") {
		t.Errorf("Referer = %q", got.Get("Referer"))
	}
	if got.Get("Cookie") != "SUB=abc" {
		t.Errorf("Cookie = %q", got.Get("Cookie"))
	}
	if !strings.Contains(got.Get("User-Agent"), "Mozilla") {
		t.Errorf("User-Agent = %q", got.Get("User-Agent"))
	}
}

func TestFetchTrimsCookie(t *testing.T) {
	// 用户从浏览器里复制出来常常带首尾换行，原样塞进请求头会让请求非法
	var got string
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(realishResponse))
	})

	if _, err := c.Fetch(context.Background(), "  SUB=abc\n"); err != nil {
		t.Fatal(err)
	}
	if got != "SUB=abc" {
		t.Errorf("Cookie = %q，期望已去掉首尾空白", got)
	}
}

// ---------- 三态判定 ----------

func TestFetchExpiredCookie(t *testing.T) {
	// HTTP 200 但结构里没有 stream_info —— 这才是"cookie 过期"的确切信号
	c, _ := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(notLoggedIn))
	})

	_, err := c.Fetch(context.Background(), "ck")
	if !errors.Is(err, ErrExpired) {
		t.Errorf("应判定为 cookie 过期，实际 %v", err)
	}
}

func TestFetchNetworkFailureIsNotExpiry(t *testing.T) {
	// 断网、被墙、微博抽风都不能算 cookie 过期，
	// 否则用户断一次网就被要求重新登录一遍
	c := &Client{BaseURL: "http://127.0.0.1:1", HTTP: &http.Client{Timeout: time.Second}}

	_, err := c.Fetch(context.Background(), "ck")
	if err == nil {
		t.Fatal("连不上应报错")
	}
	if errors.Is(err, ErrExpired) {
		t.Errorf("网络故障不应判定为 cookie 过期: %v", err)
	}
}

func TestFetchHTTPErrorIsNotExpiry(t *testing.T) {
	for _, code := range []int{500, 502, 429} {
		c, _ := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		})
		_, err := c.Fetch(context.Background(), "ck")
		if err == nil {
			t.Fatalf("HTTP %d 应报错", code)
		}
		if errors.Is(err, ErrExpired) {
			t.Errorf("HTTP %d 不应判定为 cookie 过期: %v", code, err)
		}
	}
}

func TestFetch403IsExpiry(t *testing.T) {
	// 401/403 是明确的鉴权失败，属于过期
	for _, code := range []int{401, 403} {
		c, _ := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		})
		if _, err := c.Fetch(context.Background(), "ck"); !errors.Is(err, ErrExpired) {
			t.Errorf("HTTP %d 应判定为过期，实际 %v", code, err)
		}
	}
}

func TestFetchGarbageBodyIsNotExpiry(t *testing.T) {
	// 返回一坨 HTML（比如被网关拦了）说明请求根本没到微博，不能算过期
	c, _ := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>gateway error</body></html>"))
	})
	_, err := c.Fetch(context.Background(), "ck")
	if err == nil {
		t.Fatal("非 JSON 响应应报错")
	}
	if errors.Is(err, ErrExpired) {
		t.Errorf("解析失败不应判定为过期: %v", err)
	}
}

func TestFetchEmptyCookie(t *testing.T) {
	c := &Client{}
	if _, err := c.Fetch(context.Background(), "   "); !errors.Is(err, ErrNoCookie) {
		t.Errorf("空 cookie 应返回 ErrNoCookie，实际 %v", err)
	}
}

func TestFetchNoPushAddress(t *testing.T) {
	// 有 stream_info 但拿不到推流地址，等于没用——不能让上层拿着空地址去推流
	body := `{"data":{"item":{"stream_info":{"pull_free":{"live_origin_hls_url":"https://x/y.m3u8"}}}}}`
	c, _ := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	if _, err := c.Fetch(context.Background(), "ck"); err == nil {
		t.Error("没有推流地址时应报错")
	}
}

// ---------- 资源上限 ----------

func TestFetchCapsResponseSize(t *testing.T) {
	// 对端返回无限数据时不能把内存吃光
	c, _ := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		chunk := strings.Repeat("x", 64*1024)
		for i := 0; i < 64; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	})
	c.MaxBodyBytes = 128 * 1024

	if _, err := c.Fetch(context.Background(), "ck"); err == nil {
		t.Error("超过响应体上限应报错")
	}
}

func TestFetchRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := testServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(realishResponse))
	})
	if _, err := c.Fetch(ctx, "ck"); err == nil {
		t.Error("已取消的 context 应报错")
	}
}

// ---------- 脱敏 ----------

func TestErrorsNeverLeakCookie(t *testing.T) {
	const secret = "SUB=SUPERSECRETVALUE; SUBP=alsosecret"
	for _, h := range []http.HandlerFunc{
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) },
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(notLoggedIn)) },
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) },
	} {
		c, _ := testServer(t, h)
		_, err := c.Fetch(context.Background(), secret)
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), "SUPERSECRET") {
			t.Errorf("错误信息泄漏了 cookie: %v", err)
		}
	}
}
