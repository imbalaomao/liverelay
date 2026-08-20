package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 这些用例把「安全红线」变成每次 go test 都会跑的断言，
// 而不是靠人在发布前记得手工检查一遍。

const distIndex = "frontend/dist/index.html"

func readDist(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(distIndex)
	if err != nil {
		t.Skipf("尚未构建前端（%v），跳过", err)
	}
	return string(body)
}

func TestBuiltCSPIsStrict(t *testing.T) {
	html := readDist(t)

	csp := regexp.MustCompile(`content="(default-src[^"]*)"`).FindStringSubmatch(html)
	if csp == nil {
		t.Fatal("产物里找不到 CSP，界面等于没有任何脚本来源限制")
	}
	policy := csp[1]

	// script-src 一旦允许 unsafe-inline，模板注入就能直接变成脚本执行
	if !strings.Contains(policy, "script-src 'self'") {
		t.Errorf("script-src 不是 'self': %s", policy)
	}
	if strings.Contains(policy, "script-src") &&
		regexp.MustCompile(`script-src[^;]*unsafe-inline`).MatchString(policy) {
		t.Errorf("script-src 含 unsafe-inline: %s", policy)
	}
	if regexp.MustCompile(`script-src[^;]*unsafe-eval`).MatchString(policy) {
		t.Errorf("script-src 含 unsafe-eval: %s", policy)
	}

	// dev 模式给 HMR 开的 localhost 白名单绝不能进生产产物
	if strings.Contains(policy, "localhost") {
		t.Errorf("生产 CSP 里混进了 localhost 白名单: %s", policy)
	}

	for _, must := range []string{
		"default-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-src 'none'",
	} {
		if !strings.Contains(policy, must) {
			t.Errorf("CSP 缺少 %q: %s", must, policy)
		}
	}
}

func TestFrontendNeverUsesDangerousSinks(t *testing.T) {
	// Vue 的插值会自动转义，但 v-html 不会。直播源标题、内核帮助文本
	// 都是外部输入，任何一处 v-html 都是一个现成的 XSS 入口。
	banned := regexp.MustCompile(`\bv-html\b|\.innerHTML\s*=|\bdangerouslySetInnerHTML\b|\beval\s*\(|new\s+Function\s*\(`)

	var offenders []string
	err := filepath.Walk("frontend/src", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		switch filepath.Ext(path) {
		case ".vue", ".js", ".ts":
		default:
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for i, line := range strings.Split(string(body), "\n") {
			// 注释里提到这些名字是允许的（我们正是在说明为什么不用它们）
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") ||
				strings.HasPrefix(trimmed, "<!--") {
				continue
			}
			if banned.MatchString(line) {
				offenders = append(offenders, filepath.ToSlash(path)+":"+itoa(i+1)+" "+trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("发现危险的 DOM 写入方式:\n  %s", strings.Join(offenders, "\n  "))
	}
}

func TestBundleHasNoRemoteResources(t *testing.T) {
	// 打包产物里不该有会被真正加载的远程地址：那既是供应链风险，
	// 也意味着断网时界面会坏掉。
	dir := "frontend/dist/assets"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("尚未构建前端，跳过")
	}
	// 会被加载的形式：script/link 的 src|href，或 import/fetch 一个 http(s) 地址
	loader := regexp.MustCompile(`(?i)(src|href)\s*=\s*["']https?://|import\(["']https?://|fetch\(["']https?://`)

	for _, e := range entries {
		body, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if m := loader.FindString(string(body)); m != "" {
			t.Errorf("%s 里存在远程资源加载: %q", e.Name(), m)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
