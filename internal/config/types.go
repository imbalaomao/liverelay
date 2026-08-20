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
	// YouTubeCookieFile 是 Netscape 格式的 cookies.txt 路径。
	// yt-dlp 抓 YouTube 时通常会被要求人机验证，没有它取不到流。
	YouTubeCookieFile string `json:"youtubeCookieFile"`
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
	// HasKey 只在发给界面的表单副本里用：密钥本身不回传，
	// 但界面得知道这里原本是不是设过密钥，好显示成"已设置"而不是空。
	// 不落配置文件。
	HasKey bool `json:"hasKey,omitempty"`
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
	// WeiboLive 开启后，开播时会用本地保存的微博 cookie 现取一条推流地址
	// 追加到 Targets，并给出对应的 HLS 观看链接。地址不落配置：它会变。
	WeiboLive bool `json:"weiboLive"`
}

type Config struct {
	Version  int      `json:"version"`
	Settings Settings `json:"settings"`
	Tools    []Tool   `json:"tools"`
	Tasks    []Task   `json:"tasks"`
}
