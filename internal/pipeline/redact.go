package pipeline

import "strings"

// Redact 将日志行中的密钥替换为 ***（空密钥跳过）。
func Redact(line string, secrets []string) string {
	for _, s := range secrets {
		if s != "" {
			line = strings.ReplaceAll(line, s, "***")
		}
	}
	return line
}
