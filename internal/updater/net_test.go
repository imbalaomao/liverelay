package updater

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestCheckAgainstRealGitHub 用真实的 GitHub API 验证内置来源配置。
//
// 默认跳过：测试套件不该依赖网络，GitHub 未认证接口每小时还只有 60 次配额。
// 需要时用 LIVERELAY_NET_TEST=1 go test ./internal/updater/ -run RealGitHub -v 跑。
//
// 这条用例挡的是最贵的一类错误：仓库名或文件名模式写错了，单测全绿、
// 用户点更新却永远失败。
func TestCheckAgainstRealGitHub(t *testing.T) {
	if os.Getenv("LIVERELAY_NET_TEST") == "" {
		t.Skip("未设置 LIVERELAY_NET_TEST，跳过联网测试")
	}
	for _, id := range []string{"yt-dlp", "streamlink", "ffmpeg"} {
		t.Run(id, func(t *testing.T) {
			src, ok := SourceFor(id)
			if !ok {
				t.Fatalf("%s 没有内置来源", id)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()

			rel, err := (&Updater{}).Check(ctx, src)
			if err != nil {
				t.Fatalf("查询 %s 最新发布失败: %v", id, err)
			}
			t.Logf("%s → 版本 %q 文件 %q 大小 %.1fMB 滚动 %v 校验和 %v",
				id, rel.Version, rel.AssetName, float64(rel.Size)/(1<<20),
				rel.Rolling, rel.ChecksumURL != "")

			if rel.AssetURL == "" {
				t.Errorf("%s 未匹配到可下载文件", id)
			}
			if rel.Version == "" {
				t.Errorf("%s 未解析出版本", id)
			}
			if rel.Size <= 0 {
				t.Errorf("%s 文件大小 = %d", id, rel.Size)
			}
			// yt-dlp 官方提供校验和，缺了说明匹配名写错了
			if id == "yt-dlp" && rel.ChecksumURL == "" {
				t.Error("yt-dlp 应能取到 SHA2-256SUMS")
			}
		})
	}
}
