package updater

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipBytes(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeZip(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(path, zipBytes(t, entries), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// streamlinkLikeZip 仿 streamlink 便携包：exe 旁边必须有那套内嵌 Python，
// 只抽 exe 出来会得到一个跑不起来的壳。
func streamlinkLikeZip(t *testing.T) string {
	return writeZip(t, map[string][]byte{
		"streamlink-8.5.0-1-py314-x86_64/bin/streamlink.exe":          []byte("STREAMLINK"),
		"streamlink-8.5.0-1-py314-x86_64/bin/streamlinkw.exe":         []byte("STREAMLINKW"),
		"streamlink-8.5.0-1-py314-x86_64/pkgs/python313.dll":          []byte("PYTHON"),
		"streamlink-8.5.0-1-py314-x86_64/pkgs/streamlink/__init__.py": []byte("MODULE"),
	})
}

func TestExtractTreeKeepsEverything(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out")
	if err := extractTree(streamlinkLikeZip(t), dst); err != nil {
		t.Fatalf("extractTree 失败: %v", err)
	}
	for rel, want := range map[string]string{
		"bin/streamlink.exe":          "STREAMLINK",
		"pkgs/python313.dll":          "PYTHON",
		"pkgs/streamlink/__init__.py": "MODULE",
	} {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("缺少 %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, 期望 %q", rel, got, want)
		}
	}
}

func TestExtractTreeStripsSingleTopLevelDir(t *testing.T) {
	// 包里恒有一层带版本号的顶层目录。不剥掉的话，每次更新后
	// 可执行文件的路径都会变，配置里存的路径立刻失效
	dst := filepath.Join(t.TempDir(), "out")
	if err := extractTree(streamlinkLikeZip(t), dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "streamlink-8.5.0-1-py314-x86_64")); !os.IsNotExist(err) {
		t.Error("顶层目录未被剥掉")
	}
}

func TestExtractTreeKeepsMultipleTopLevelDirs(t *testing.T) {
	// 有多个顶层条目时不能乱剥，否则会把内容搅在一起
	zipPath := writeZip(t, map[string][]byte{
		"a/x.txt": []byte("A"),
		"b/y.txt": []byte("B"),
	})
	dst := filepath.Join(t.TempDir(), "out")
	if err := extractTree(zipPath, dst); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a/x.txt", "b/y.txt"} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(rel))); err != nil {
			t.Errorf("缺少 %s", rel)
		}
	}
}

func TestExtractTreeRejectsPathTraversal(t *testing.T) {
	// 整包展开是按包内路径落盘的，zip-slip 在这里才真正有杀伤力
	for _, bad := range []string{"../evil.exe", "a/../../evil.exe"} {
		t.Run(bad, func(t *testing.T) {
			zipPath := writeZip(t, map[string][]byte{bad: []byte("EVIL")})
			dst := filepath.Join(t.TempDir(), "out")
			if err := extractTree(zipPath, dst); err == nil {
				t.Error("含路径穿越的条目必须拒绝")
			}
		})
	}
}

func TestExtractTreeRejectsAbsolutePath(t *testing.T) {
	zipPath := writeZip(t, map[string][]byte{"/etc/passwd": []byte("X")})
	dst := filepath.Join(t.TempDir(), "out")
	if err := extractTree(zipPath, dst); err == nil {
		t.Error("绝对路径条目必须拒绝")
	}
}

func TestExtractTreeCapsTotalSize(t *testing.T) {
	// zip 炸弹：单个条目不大，但加起来能把磁盘撑爆
	entries := map[string][]byte{}
	for i := 0; i < 8; i++ {
		entries[string(rune('a'+i))+".bin"] = bytes.Repeat([]byte("A"), 512<<10)
	}
	dst := filepath.Join(t.TempDir(), "out")
	err := extractTreeLimit(writeZip(t, entries), dst, 1<<20, 1000)
	if err == nil {
		t.Fatal("解压总量超限必须拒绝")
	}
	if !strings.Contains(err.Error(), "上限") {
		t.Errorf("错误信息应说明是超限: %v", err)
	}
	// 半成品必须清掉，不能留一堆解了一半的文件
	if _, serr := os.Stat(dst); !os.IsNotExist(serr) {
		t.Error("超限时不应留下半个目录")
	}
}

func TestExtractTreeCapsEntryCount(t *testing.T) {
	entries := map[string][]byte{}
	for i := 0; i < 50; i++ {
		entries[filepath.Join("d", string(rune('a'+i%26))+string(rune('a'+i/26))+".txt")] = []byte("x")
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := extractTreeLimit(writeZip(t, entries), dst, 1<<30, 10); err == nil {
		t.Error("条目数超限必须拒绝")
	}
}

// ---------- 目录换入 ----------

func mkTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstallTreeSwaps(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "streamlink")
	staged := filepath.Join(dir, "staged")
	mkTree(t, target, map[string]string{"bin/streamlink.exe": "OLD", "pkgs/old.py": "OLD"})
	mkTree(t, staged, map[string]string{"bin/streamlink.exe": "NEW", "pkgs/new.py": "NEW"})

	if err := installTree(staged, target); err != nil {
		t.Fatalf("installTree 失败: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(target, "bin", "streamlink.exe")); string(got) != "NEW" {
		t.Errorf("可执行文件 = %q", got)
	}
	// 旧版本的 python 包必须整个消失，不能和新版本混在一起
	if _, err := os.Stat(filepath.Join(target, "pkgs", "old.py")); !os.IsNotExist(err) {
		t.Error("旧目录内容残留，新旧 python 包混在一起会出难查的问题")
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Error("成功后应清理 .old 备份")
	}
}

func TestInstallTreeRollsBack(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "streamlink")
	mkTree(t, target, map[string]string{"bin/streamlink.exe": "OLD"})

	if err := installTree(filepath.Join(dir, "并不存在"), target); err == nil {
		t.Fatal("换入不存在的目录应报错")
	}
	got, err := os.ReadFile(filepath.Join(target, "bin", "streamlink.exe"))
	if err != nil {
		t.Fatalf("原目录应被恢复: %v", err)
	}
	if string(got) != "OLD" {
		t.Errorf("回滚后内容 = %q", got)
	}
}

func TestInstallTreeFirstTime(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "streamlink")
	staged := filepath.Join(dir, "staged")
	mkTree(t, staged, map[string]string{"bin/streamlink.exe": "NEW"})

	if err := installTree(staged, target); err != nil {
		t.Fatalf("首次安装不应失败: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(target, "bin", "streamlink.exe")); string(got) != "NEW" {
		t.Errorf("可执行文件 = %q", got)
	}
}
