package config

import (
	"encoding/json"
	"errors"
	"os"
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
		},
		Tools: []Tool{
			{ID: "streamlink", Name: "streamlink", Builtin: true, Path: "tools/streamlink.exe", Role: "fetch",
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
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	if c, jerr := parse(data); jerr == nil {
		return c, nil
	} else if bak, berr := os.ReadFile(path + ".bak"); berr == nil {
		if c, jerr2 := parse(bak); jerr2 == nil {
			return c, nil
		}
		return nil, jerr
	} else {
		return nil, jerr
	}
}

func parse(data []byte) (*Config, error) {
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
	return &c, nil
}

func Save(path string, c *Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
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
