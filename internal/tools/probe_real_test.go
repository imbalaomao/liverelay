package tools

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestProbeRealTools 用机器上真实存在的内核验证探测逻辑。
// 找不到的工具直接跳过——这些是可选依赖，不该让别人的机器上测试变红。
func TestProbeRealTools(t *testing.T) {
	cases := []struct {
		bin      string
		wantCaps []string
	}{
		// ffmpeg 的版本号在 "ffmpeg version N.N.N-..." 里，帮助里有 pipe: 输出说明
		{"ffmpeg", nil},
		// yt-dlp 是日期式版本号（2024.04.09），且必须能认出 --proxy 与 --dump-json，
		// 否则 M3-4 的无人值守探测会以为它不支持 JSON
		{"yt-dlp", []string{CapProxy, CapJSON}},
		{"streamlink", []string{CapStdout, CapProxy, CapJSON}},
		{"N_m3u8DL-RE", nil},
	}

	for _, c := range cases {
		t.Run(c.bin, func(t *testing.T) {
			path, err := exec.LookPath(c.bin)
			if err != nil {
				t.Skipf("未安装 %s，跳过", c.bin)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()

			info, err := (&Prober{}).Probe(ctx, path)
			if err != nil {
				t.Fatalf("探测 %s 失败: %v", c.bin, err)
			}
			t.Logf("%s → 版本 %q 能力 %v 选项 %d 摘要 %q",
				c.bin, info.Version, info.Caps, info.Flags, info.Summary)

			if info.Version == "" {
				t.Errorf("%s 没能识别出版本号", c.bin)
			}
			for _, want := range c.wantCaps {
				if !hasCap(info.Caps, want) {
					t.Errorf("%s 应识别出能力 %q，实际 %v", c.bin, want, info.Caps)
				}
			}
		})
	}
}
