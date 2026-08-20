package weibo

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CookieFileName 是 cookie 落盘的文件名，位于数据根下。
//
// 有意不放进 config.json：那个文件用户会手改、会随便携目录被拷走、
// 也可能被顺手贴到聊天里求助。微博 cookie 等同于账号本身，不能混在里面。
const CookieFileName = "weibo_cookie.bin"

// envelopeVersion 是存储信封的版本号，留给以后换加密方案时识别。
const envelopeVersion = 1

// scopeDPAPI 表示内容由 Windows DPAPI 加密，绑定当前 Windows 账户。
const scopeDPAPI = "dpapi"

// envelope 是落盘的自描述信封。带上方案名，是为了让一份从别的平台
// 或别的机器来的文件能给出"请重新录入"而不是一串看不懂的解密错误。
type envelope struct {
	Version int    `json:"v"`
	Scope   string `json:"scope"`
	Data    string `json:"data"`
}

// Store 负责 cookie 的本地加密存取。
type Store struct {
	dir string
	// protect / unprotect 可注入，测试用；默认走平台实现。
	protect   func([]byte) ([]byte, error)
	unprotect func([]byte) ([]byte, error)
}

func NewStore(dataDir string) *Store {
	return &Store{dir: dataDir, protect: protectData, unprotect: unprotectData}
}

// Path 返回 cookie 文件的完整路径。
func (s *Store) Path() string { return filepath.Join(s.dir, CookieFileName) }

// Exists 报告是否已存过 cookie。注意它只看文件在不在，
// 文件能不能解开要等到 Load。
func (s *Store) Exists() bool {
	st, err := os.Stat(s.Path())
	return err == nil && !st.IsDir()
}

// Save 加密保存 cookie。
func (s *Store) Save(cookie string) error {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return ErrNoCookie
	}
	sealed, err := s.protect([]byte(cookie))
	if err != nil {
		return fmt.Errorf("加密 cookie 失败: %w", err)
	}
	body, err := json.Marshal(envelope{
		Version: envelopeVersion,
		Scope:   storageScope,
		Data:    base64.StdEncoding.EncodeToString(sealed),
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}

	// 先写临时文件再改名：写到一半崩掉不能把已有的 cookie 毁掉
	tmp := s.Path() + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	// Windows 的 Rename 覆盖不了已存在的文件
	if err := os.Remove(s.Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.Path()); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Load 读出 cookie。
//
// 任何"读不出来"的情形——文件不存在、格式不认识、解不开——一律归为
// ErrNoCookie。因为对用户来说这些都是同一件事：请重新录入。
// 尤其是便携目录被拷到另一台机器时，DPAPI 必然解不开，那不是故障。
func (s *Store) Load() (string, error) {
	body, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoCookie
	}
	if err != nil {
		return "", err
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return "", fmt.Errorf("%w（本地保存的凭据已损坏）", ErrNoCookie)
	}
	if env.Scope != storageScope {
		return "", fmt.Errorf("%w（本地凭据由 %q 方式保存，本机读不了）", ErrNoCookie, env.Scope)
	}
	sealed, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return "", fmt.Errorf("%w（本地保存的凭据已损坏）", ErrNoCookie)
	}
	plain, err := s.unprotect(sealed)
	if err != nil {
		// 换了机器或换了 Windows 账户就会走到这里
		return "", fmt.Errorf("%w（本地凭据无法在本机解开，可能换过设备或系统账户）", ErrNoCookie)
	}
	cookie := strings.TrimSpace(string(plain))
	if cookie == "" {
		return "", ErrNoCookie
	}
	return cookie, nil
}

// Clear 删除已保存的 cookie。文件本就不存在时是空操作。
func (s *Store) Clear() error {
	err := os.Remove(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
