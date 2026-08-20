package updater

import (
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imbalaomao/liverelay/internal/config"
	"github.com/imbalaomao/liverelay/internal/tools"
)

// TestDefaultPathsMatchInstallLayout 盯住配置默认路径与更新器落地位置的一致性。
//
// 这两处分散在不同的包里，很容易各改各的：streamlink 从"单个 exe"改成
// "整包落地"时就走散过一次——默认路径还是 tools/streamlink.exe，而更新器
// 装到了 tools/streamlink/bin/streamlink.exe，用户更新完反而用不了了。
func TestDefaultPathsMatchInstallLayout(t *testing.T) {
	cfg := config.Default()

	for _, tool := range cfg.Tools {
		src, ok := SourceFor(tool.ID)
		if !ok {
			continue // 没有更新来源的内核不受此约束
		}
		t.Run(tool.ID, func(t *testing.T) {
			var want string
			switch src.Layout {
			case LayoutWholeTree:
				want = path.Join("tools", src.ToolID, src.Binary)
			default:
				want = path.Join("tools", path.Base(src.Binary))
			}
			if tool.Path != want {
				t.Errorf("默认路径 %q 与更新器落地位置 %q 不一致——更新一次用户就用不了了",
					tool.Path, want)
			}
		})
	}
}

// TestUpdateResultMatchesDefaultPath 从另一头确认：更新器算出来的 BinaryPath
// 相对数据根之后，正好等于配置里的默认路径。
func TestUpdateResultMatchesDefaultPath(t *testing.T) {
	const dataDir = `D:\app\data`
	toolsDir := filepath.Join(dataDir, "tools")
	cfg := config.Default()

	for _, tool := range cfg.Tools {
		src, ok := SourceFor(tool.ID)
		if !ok {
			continue
		}
		t.Run(tool.ID, func(t *testing.T) {
			var binaryPath string
			switch src.Layout {
			case LayoutWholeTree:
				binaryPath = filepath.Join(toolsDir, src.ToolID, filepath.FromSlash(src.Binary))
			default:
				binaryPath = filepath.Join(toolsDir, path.Base(src.Binary))
			}
			// 配置里存的是相对数据根的路径，解析后应当指向同一个文件
			resolved := tools.Resolve(dataDir, tool.Path)
			if !strings.EqualFold(resolved, binaryPath) {
				t.Errorf("配置解析出 %q，更新器落地在 %q", resolved, binaryPath)
			}
		})
	}
}
