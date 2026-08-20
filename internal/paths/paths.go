// Package paths 判定数据根目录并维护标准目录布局（规格 §6）。
package paths

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// appDirName 是安装模式下 %APPDATA% 内的目录名。
const appDirName = "LiveRelay"

// Mode 是数据存放模式。
type Mode string

const (
	// Portable 便携模式：一切数据存 exe 同级 data/，零注册表写入，删目录即卸载。
	Portable Mode = "portable"
	// Installed 安装模式：数据存 %APPDATA%/LiveRelay。
	Installed Mode = "installed"
)

func (m Mode) String() string { return string(m) }

// Display 返回中文模式名，供 UI 直接显示。
func (m Mode) Display() string {
	if m == Portable {
		return "便携"
	}
	return "安装"
}

// EnvDataDir 是显式指定数据根的环境变量名。
//
// 开发时 exe 位于 build/bin 下，按便携规则找的是 build/bin/data，
// 看不见仓库根的 data/；打包发行后用户想把几百 MB 的内核与录像放到别的盘
// 也需要这个口子。优先级高于其余一切判定。
const EnvDataDir = "LIVERELAY_DATA"

// resolve 是判定逻辑的纯函数内核（规格 §6.1）：
// 显式指定 → 用它；否则 exe 同级存在 data/ 目录 → 便携模式；
// 再否则落到 %APPDATA%/LiveRelay。
// APPDATA 缺失时退回便携，绝不返回空路径——否则数据会落到进程当前目录。
func resolve(exeDir, appData, override string) (string, Mode) {
	// 空白等同于没设：否则 set LIVERELAY_DATA= 会把数据根变成当前工作目录
	if o := strings.TrimSpace(override); o != "" {
		return o, Portable
	}
	portable := filepath.Join(exeDir, "data")
	if st, err := os.Stat(portable); err == nil && st.IsDir() {
		return portable, Portable
	}
	if appData == "" {
		return portable, Portable
	}
	return filepath.Join(appData, appDirName), Installed
}

// Root 返回当前进程应使用的数据根与模式。
func Root() (string, Mode, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("定位可执行文件: %w", err)
	}
	root, mode := resolve(filepath.Dir(exe), os.Getenv("APPDATA"), os.Getenv(EnvDataDir))
	return root, mode, nil
}

func Tools(root string) string      { return filepath.Join(root, "tools") }
func Logs(root string) string       { return filepath.Join(root, "logs") }
func Recordings(root string) string { return filepath.Join(root, "recordings") }
func Cache(root string) string      { return filepath.Join(root, "cache") }
func ConfigFile(root string) string { return filepath.Join(root, "config.json") }

// Ensure 创建数据根及标准子目录，幂等。
func Ensure(root string) error {
	for _, d := range []string{root, Tools(root), Logs(root), Recordings(root), Cache(root)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("创建目录 %s: %w", d, err)
		}
	}
	return nil
}

// MakePortable 在 exe 同级建立 data/ 并把 srcRoot 的配置复制过去，返回新数据根。
// 用复制而非移动：迁移中途失败时原配置必须完好，否则用户配置会丢。
// 目标已有配置时保留不动（重复点击"转为便携"不应覆盖）。
func MakePortable(exeDir, srcRoot string) (string, error) {
	dst := filepath.Join(exeDir, "data")
	if err := Ensure(dst); err != nil {
		return "", err
	}
	if _, err := os.Stat(ConfigFile(dst)); err == nil {
		return dst, nil // 已是便携模式，保留现有配置
	}
	if err := copyFile(ConfigFile(srcRoot), ConfigFile(dst)); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return dst, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
