package pipeline

import (
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`a b c`, []string{"a", "b", "c"}},
		{`--key "hello world"`, []string{"--key", "hello world"}},
		{`--key='a b'`, []string{"--key=a b"}},
		{`a\ b`, []string{"a b"}},
		{`  --x   y  `, []string{"--x", "y"}},
		{`""`, []string{""}},
		{`--save-dir "D:\rec\new"`, []string{"--save-dir", `D:\rec\new`}},
		{`--key 'C:\path with space'`, []string{"--key", `C:\path with space`}},
		{`"a\"b"`, []string{`a\"b`}},
		{``, nil},
		{`   `, nil},
	}
	for _, c := range cases {
		got, err := Tokenize(c.in)
		if err != nil {
			t.Errorf("Tokenize(%q) 报错: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Tokenize(%q) = %v, 期望 %v", c.in, got, c.want)
		}
	}
	if _, err := Tokenize(`"unclosed`); err == nil {
		t.Error("未闭合引号应报错")
	}
}

func TestRenderTemplate(t *testing.T) {
	vars := map[string]string{"url": "https://x/1", "quality": "best", "proxy": "", "outdir": "rec/abc"}
	got := RenderTemplate([]string{"{url}", "{quality}", "-O"}, vars)
	want := []string{"https://x/1", "best", "-O"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RenderTemplate = %v, 期望 %v", got, want)
	}
}

func TestRedact(t *testing.T) {
	line := `推流至 rtmp://a/b/secret_key_123 失败`
	got := Redact(line, []string{"secret_key_123"})
	want := `推流至 rtmp://a/b/*** 失败`
	if got != want {
		t.Fatalf("Redact = %q, 期望 %q", got, want)
	}
	if Redact("abc", []string{""}) != "abc" {
		t.Fatal("空密钥不应参与替换")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"XX直播":        "XX直播",
		`a/b\c:d*e?f`: "a_b_c_d_e_f",
		"":            "",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, 期望 %q", in, got, want)
		}
	}
	long := make([]rune, 100)
	for i := range long {
		long[i] = 'a'
	}
	if got := []rune(SanitizeName(string(long))); len(got) != 64 {
		t.Errorf("应截断为 64 字符, 实际 %d", len(got))
	}
}
