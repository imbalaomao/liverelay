package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
)

// MaxConcurrentCap 是全局并发推流上限的硬上界（规格 §4.5：可配 1–16）。
// 配置文件被手工改成超大值时必须钳制，否则会拉起数十个 ffmpeg 拖垮目标机。
const MaxConcurrentCap = 16

func Default() *Config {
	return &Config{
		Version: 1,
		Settings: Settings{
			MaxConcurrent: 4, CloseToTray: true, PreventSleep: true,
			Theme: "dark", ProbeIntervalSec: 60,
			// 类型给个有效默认值：留空会让设置页的下拉框选不中任何一项，
			// 看起来像是界面坏了
			Proxy: ProxySettings{Type: "http"},
		},
		Tools: []Tool{
			// streamlink 的便携版是"整个目录"而不是单个 exe：exe 依赖同目录下的
			// 内嵌 Python。默认路径必须与更新器整包落地的位置一致，否则第一次
			// 更新完，配置里的路径就指向一个不存在的文件。
			// internal/updater 里有一条测试专门盯着这两处别走散。
			{ID: "streamlink", Name: "streamlink", Builtin: true, Path: "tools/streamlink/bin/streamlink.exe", Role: "fetch",
				ArgTemplate:   []string{"{url}", "{quality}", "-O"},
				ProbeTemplate: []string{"--json", "{url}"}},
			{ID: "yt-dlp", Name: "yt-dlp", Builtin: true, Path: "tools/yt-dlp.exe", Role: "fetch",
				ArgTemplate:   []string{"-o", "-", "{url}"},
				ProbeTemplate: []string{"--dump-json", "--no-download", "{url}"}},
			// ffmpeg 只做推流与录制出口，不承担探测开播的职责
			{ID: "ffmpeg", Name: "ffmpeg", Builtin: true, Path: "tools/ffmpeg.exe", Role: "both"},
		},
	}
}

func Load(path string) (*Config, error) {
	// #nosec G304 -- path 由数据根拼常量文件名得到，无外部可控片段
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	if c, jerr := parse(data); jerr == nil {
		return c, nil
		// #nosec G304 -- 同上，只是换成 .bak 后缀
	} else if bak, berr := os.ReadFile(path + ".bak"); berr == nil {
		if c, jerr2 := parse(bak); jerr2 == nil {
			return c, nil
		}
		return nil, jerr
	} else {
		return nil, jerr
	}
}

// utf8BOM 是 UTF-8 字节序标记。记事本、VS Code 的"UTF-8 with BOM"、
// PowerShell 的 Set-Content -Encoding UTF8 都会写出它，而 encoding/json
// 见到 BOM 直接报错——用户手改一次 config.json 就被静默重置回默认配置。
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func parse(data []byte) (*Config, error) {
	data = bytes.TrimPrefix(bytes.TrimLeft(data, " \t\r\n"), utf8BOM)

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Settings.MaxConcurrent <= 0 {
		c.Settings.MaxConcurrent = 4
	}
	if c.Settings.MaxConcurrent > MaxConcurrentCap {
		c.Settings.MaxConcurrent = MaxConcurrentCap
	}
	if c.Settings.ProbeIntervalSec < 30 {
		c.Settings.ProbeIntervalSec = 60
	}
	if c.Settings.ProbeIntervalSec > 300 {
		c.Settings.ProbeIntervalSec = 300
	}
	// 归一化代理类型：老配置里可能是空的或大小写不一致，
	// 设置页的下拉框只认这两个值，对不上就会显示成一片空白
	switch strings.ToLower(strings.TrimSpace(c.Settings.Proxy.Type)) {
	case "socks5":
		c.Settings.Proxy.Type = "socks5"
	default:
		c.Settings.Proxy.Type = "http"
	}
	return &c, nil
}

func Save(path string, c *Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// #nosec G703 G304 -- path 由数据根拼上常量文件名得到，数据根是用户为自己
	// 这台机器选定的目录，没有外部可控的路径片段
	if old, err := os.ReadFile(path); err == nil {
		if werr := os.WriteFile(path+".bak", old, 0o600); werr != nil {
			return werr
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	// Windows 的 os.Rename 无法覆盖已存在文件，先删后改名
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmp, path)
}
