package config

type ProxySettings struct {
	Enabled  bool   `json:"enabled"`
	Type     string `json:"type"` // "http" | "socks5"
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type Settings struct {
	Proxy            ProxySettings `json:"proxy"`
	MaxConcurrent    int           `json:"maxConcurrent"`
	CloseToTray      bool          `json:"closeToTray"`
	PreventSleep     bool          `json:"preventSleep"`
	Theme            string        `json:"theme"`
	RecordDir        string        `json:"recordDir"`
	ProbeIntervalSec int           `json:"probeIntervalSec"`
}

type Tool struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Builtin      bool     `json:"builtin"`
	Path         string   `json:"path"`
	PathOverride string   `json:"pathOverride"`
	Version      string   `json:"version"`
	CapSummary   string   `json:"capSummary"`
	Role         string   `json:"role"` // "fetch" | "record" | "both"
	ArgTemplate  []string `json:"argTemplate"`
	// ProbeTemplate 是"探测源站是否开播"用的参数模板。为空表示该内核无法用于
	// 无人值守——各家工具的探测方式没有通用写法，猜一个只会得到误判。
	ProbeTemplate []string `json:"probeTemplate"`
}

func (t Tool) EffectivePath() string {
	if t.PathOverride != "" {
		return t.PathOverride
	}
	return t.Path
}

type Target struct {
	Proto string `json:"proto"` // "rtmp" | "srt" | "udp" | "hls"
	URL   string `json:"url"`
	Key   string `json:"key"`
}

type Task struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	SourceURL    string   `json:"sourceUrl"`
	ToolID       string   `json:"toolId"`
	Quality      string   `json:"quality"`
	Targets      []Target `json:"targets"`
	Unattended   bool     `json:"unattended"`
	AutoRecord   bool     `json:"autoRecord"`
	RecordToolID string   `json:"recordToolId"`
	CustomArgs   string   `json:"customArgs"`
}

type Config struct {
	Version  int      `json:"version"`
	Settings Settings `json:"settings"`
	Tools    []Tool   `json:"tools"`
	Tasks    []Task   `json:"tasks"`
}
