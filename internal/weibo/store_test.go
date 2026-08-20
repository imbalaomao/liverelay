package weibo

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const sampleCookie = "SUB=_2A25KsuperSECRETvalue; SUBP=0033WrSXqPxfM; XSRF-TOKEN=abc"

// fakeCrypto 是可逆的假加密：只把字节反过来。
// 用它验证信封逻辑，真实 DPAPI 另有一条用例。
func fakeCrypto() (protect, unprotect func([]byte) ([]byte, error)) {
	rev := func(b []byte) ([]byte, error) {
		out := make([]byte, len(b))
		for i := range b {
			out[i] = b[len(b)-1-i]
		}
		return out, nil
	}
	return rev, rev
}

func testStore(t *testing.T) *Store {
	t.Helper()
	p, u := fakeCrypto()
	s := NewStore(t.TempDir())
	s.protect, s.unprotect = p, u
	return s
}

// ---------- 基本读写 ----------

func TestSaveLoadRoundTrip(t *testing.T) {
	s := testStore(t)
	if err := s.Save(sampleCookie); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if got != sampleCookie {
		t.Errorf("Load = %q", got)
	}
}

func TestLoadWithoutFile(t *testing.T) {
	s := testStore(t)
	if _, err := s.Load(); !errors.Is(err, ErrNoCookie) {
		t.Errorf("没有文件时应返回 ErrNoCookie，实际 %v", err)
	}
	if s.Exists() {
		t.Error("Exists 应为 false")
	}
}

func TestSaveTrimsAndRejectsEmpty(t *testing.T) {
	s := testStore(t)
	if err := s.Save("   \n "); !errors.Is(err, ErrNoCookie) {
		t.Errorf("空白 cookie 应被拒绝，实际 %v", err)
	}
	if s.Exists() {
		t.Error("被拒绝的保存不应留下文件")
	}

	if err := s.Save("  " + sampleCookie + "\n"); err != nil {
		t.Fatal(err)
	}
	// 用户从浏览器复制常带首尾换行，存进去再原样塞进请求头会让请求非法
	if got, _ := s.Load(); got != sampleCookie {
		t.Errorf("Load = %q，期望已去掉首尾空白", got)
	}
}

func TestSaveOverwrites(t *testing.T) {
	s := testStore(t)
	if err := s.Save("old=1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("new=2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load(); got != "new=2" {
		t.Errorf("Load = %q", got)
	}
}

func TestClear(t *testing.T) {
	s := testStore(t)
	if err := s.Save(sampleCookie); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear 失败: %v", err)
	}
	if s.Exists() {
		t.Error("Clear 后文件应已删除")
	}
	// 重复 Clear 不应报错：用户可能连点两下"清除"
	if err := s.Clear(); err != nil {
		t.Errorf("重复 Clear 应是空操作，实际 %v", err)
	}
}

// ---------- 落盘形态 ----------

func TestFileNeverContainsPlaintextCookie(t *testing.T) {
	// 这条是整个存储设计的目的：cookie 等同于账号本身，
	// 明文落在一个会被随手备份、随便携目录拷走的文件里风险太大
	s := testStore(t)
	if err := s.Save(sampleCookie); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "superSECRETvalue") {
		t.Errorf("落盘文件里出现了明文 cookie:\n%s", raw)
	}
}

func TestFilePermissionsAreRestrictive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不用 Unix 权限位表达访问控制")
	}
	s := testStore(t)
	if err := s.Save(sampleCookie); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Errorf("权限 %v 对同组或其他用户可读", st.Mode().Perm())
	}
}

func TestSaveIsAtomic(t *testing.T) {
	// 写到一半崩掉不能把已有的 cookie 毁掉，所以先写临时文件再改名
	s := testStore(t)
	if err := s.Save(sampleCookie); err != nil {
		t.Fatal(err)
	}
	s.protect = func([]byte) ([]byte, error) { return nil, errors.New("加密失败") }
	if err := s.Save("new=2"); err == nil {
		t.Fatal("加密失败时 Save 应报错")
	}

	_, u := fakeCrypto()
	s.unprotect = u
	if got, _ := s.Load(); got != sampleCookie {
		t.Errorf("保存失败后原 cookie 应完好，实际 %q", got)
	}
	// 不能留下临时文件
	entries, _ := os.ReadDir(filepath.Dir(s.Path()))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
}

// ---------- 损坏与跨机 ----------

func TestLoadCorruptedFile(t *testing.T) {
	s := testStore(t)
	if err := os.WriteFile(s.Path(), []byte("这不是我们的格式"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Load()
	if err == nil {
		t.Fatal("损坏的文件应报错")
	}
	if !errors.Is(err, ErrNoCookie) {
		t.Errorf("损坏的文件应等同于未录入（提示重新录入），实际 %v", err)
	}
}

func TestLoadUndecryptableFile(t *testing.T) {
	// 便携目录被拷到另一台机器或另一个 Windows 账户时，DPAPI 解不开。
	// 这不是错误状态，而是"请重新录入"——不能让用户对着一个报错束手无策
	s := testStore(t)
	if err := s.Save(sampleCookie); err != nil {
		t.Fatal(err)
	}
	s.unprotect = func([]byte) ([]byte, error) { return nil, errors.New("解密失败") }

	_, err := s.Load()
	if !errors.Is(err, ErrNoCookie) {
		t.Errorf("解不开的文件应等同于未录入，实际 %v", err)
	}
}

func TestLoadUnknownScope(t *testing.T) {
	// 别的平台存的信封拿到 Windows 上（或反过来）也要给出可理解的结果
	s := testStore(t)
	body := `{"v":1,"scope":"未来的某种方案","data":"YWJj"}`
	if err := os.WriteFile(s.Path(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNoCookie) {
		t.Errorf("无法识别的存储方案应等同于未录入，实际 %v", err)
	}
}

// ---------- 真实 DPAPI ----------

func TestRealDPAPIRoundTrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI 仅 Windows 可用")
	}
	s := NewStore(t.TempDir()) // 不注入假加密，走真实 DPAPI
	if err := s.Save(sampleCookie); err != nil {
		t.Fatalf("真实 DPAPI 保存失败: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("真实 DPAPI 读取失败: %v", err)
	}
	if got != sampleCookie {
		t.Errorf("Load = %q", got)
	}
	raw, _ := os.ReadFile(s.Path())
	if strings.Contains(string(raw), "superSECRETvalue") {
		t.Error("DPAPI 加密后文件里仍能看到明文 cookie")
	}
}
