package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrefersPortableWhenDataDirExists(t *testing.T) {
	exeDir := t.TempDir()
	portable := filepath.Join(exeDir, "data")
	if err := os.Mkdir(portable, 0o755); err != nil {
		t.Fatal(err)
	}
	root, mode := resolve(exeDir, filepath.Join(t.TempDir(), "AppData"))
	if mode != Portable {
		t.Fatalf("exe 同级存在 data/ 时应为便携模式，得到 %s", mode)
	}
	if root != portable {
		t.Fatalf("便携模式数据根应为 %s，得到 %s", portable, root)
	}
}

func TestResolveFallsBackToAppData(t *testing.T) {
	exeDir := t.TempDir() // 不创建 data/
	appData := t.TempDir()
	root, mode := resolve(exeDir, appData)
	if mode != Installed {
		t.Fatalf("无 data/ 时应为安装模式，得到 %s", mode)
	}
	if want := filepath.Join(appData, appDirName); root != want {
		t.Fatalf("安装模式数据根应为 %s，得到 %s", want, root)
	}
}

// APPDATA 缺失（非常规环境）时不能返回空路径，否则会把数据写到进程当前目录。
func TestResolveWithoutAppDataFallsBackToExeDir(t *testing.T) {
	exeDir := t.TempDir()
	root, mode := resolve(exeDir, "")
	if mode != Portable {
		t.Fatalf("APPDATA 缺失时应退回便携模式，得到 %s", mode)
	}
	if want := filepath.Join(exeDir, "data"); root != want {
		t.Fatalf("应退回 exe 同级 data/，得到 %s", root)
	}
}

// data 是文件而非目录时不得误判为便携模式。
func TestResolveIgnoresDataFile(t *testing.T) {
	exeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(exeDir, "data"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	appData := t.TempDir()
	_, mode := resolve(exeDir, appData)
	if mode != Installed {
		t.Fatalf("data 为普通文件时应为安装模式，得到 %s", mode)
	}
}

func TestEnsureCreatesLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "LiveRelay")
	if err := Ensure(root); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, d := range []string{Tools(root), Logs(root), Recordings(root), Cache(root)} {
		st, err := os.Stat(d)
		if err != nil {
			t.Fatalf("子目录未创建: %s (%v)", d, err)
		}
		if !st.IsDir() {
			t.Fatalf("%s 不是目录", d)
		}
	}
	if want := filepath.Join(root, "config.json"); ConfigFile(root) != want {
		t.Fatalf("配置路径应为 %s，得到 %s", want, ConfigFile(root))
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "LiveRelay")
	for i := 0; i < 2; i++ {
		if err := Ensure(root); err != nil {
			t.Fatalf("第 %d 次 Ensure: %v", i+1, err)
		}
	}
}

func TestMakePortableMigratesConfig(t *testing.T) {
	src := filepath.Join(t.TempDir(), "installed")
	if err := Ensure(src); err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"version":1}`)
	if err := os.WriteFile(ConfigFile(src), want, 0o600); err != nil {
		t.Fatal(err)
	}

	exeDir := t.TempDir()
	dst, err := MakePortable(exeDir, src)
	if err != nil {
		t.Fatalf("MakePortable: %v", err)
	}
	if dst != filepath.Join(exeDir, "data") {
		t.Fatalf("便携数据根应为 exe 同级 data/，得到 %s", dst)
	}
	got, err := os.ReadFile(ConfigFile(dst))
	if err != nil {
		t.Fatalf("配置未迁移: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("配置内容不一致: %s", got)
	}
	// 迁移是复制而非移动：失败时原配置必须还在，否则用户配置会丢
	if _, err := os.Stat(ConfigFile(src)); err != nil {
		t.Fatalf("原配置应保留: %v", err)
	}
}

// 已经是便携模式时重复点击"转为便携"不得覆盖已有配置。
func TestMakePortableDoesNotOverwriteExisting(t *testing.T) {
	exeDir := t.TempDir()
	dst := filepath.Join(exeDir, "data")
	if err := Ensure(dst); err != nil {
		t.Fatal(err)
	}
	keep := []byte(`{"version":1,"keep":true}`)
	if err := os.WriteFile(ConfigFile(dst), keep, 0o600); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(t.TempDir(), "installed")
	if err := Ensure(src); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigFile(src), []byte(`{"version":1,"other":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := MakePortable(exeDir, src); err != nil {
		t.Fatalf("MakePortable: %v", err)
	}
	got, _ := os.ReadFile(ConfigFile(dst))
	if string(got) != string(keep) {
		t.Fatalf("已有便携配置被覆盖: %s", got)
	}
}
