package pipeline

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var placeholderRe = regexp.MustCompile(`\{(url|quality|proxy|outdir)\}`)

// RenderTemplate 将参数模板中的 {url}/{quality}/{proxy}/{outdir} 占位符替换为实际值。
func RenderTemplate(tmpl []string, vars map[string]string) []string {
	out := make([]string, 0, len(tmpl))
	for _, e := range tmpl {
		out = append(out, placeholderRe.ReplaceAllStringFunc(e, func(m string) string {
			return vars[m[1:len(m)-1]]
		}))
	}
	return out
}

// Tokenize 引号感知分词：支持单/双引号包裹含空格参数、反斜杠转义。
// 结果作为参数数组逐字传递，全程无 shell 拼接。
func Tokenize(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	var quote rune // 0 表示不在引号内
	escaped := false
	hasToken := false

	flush := func() {
		if hasToken {
			args = append(args, cur.String())
			cur.Reset()
			hasToken = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
			hasToken = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			hasToken = true
		case r == '"' || r == '\'':
			quote = r
			hasToken = true
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
			hasToken = true
		}
	}
	if escaped {
		cur.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("自定义参数存在未闭合引号")
	}
	flush()
	return args, nil
}

// SanitizeName 任务名 → 安全目录名：仅保留字母（含中文）、数字、-、_，其余替换为 _，截断 64 字符。
func SanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := b.String()
	if utf8.RuneCountInString(s) > 64 {
		s = string([]rune(s)[:64])
	}
	return s
}
