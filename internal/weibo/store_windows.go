//go:build windows

package weibo

import (
	"fmt"
	"syscall"
	"unsafe"
)

// storageScope 标明本机用的是 DPAPI。
const storageScope = scopeDPAPI

// cryptProtectUIForbidden 禁止 DPAPI 弹任何界面。
// 我们可能在后台线程里解密，弹窗会让程序看起来卡死。
const cryptProtectUIForbidden = 0x1

// entropy 是附加熵。它不是秘密（就在二进制里躺着），但能让通用的
// DPAPI 解密工具没法直接把这个文件读出来——多一道门槛而已。
var entropy = []byte("LiveRelay/weibo-cookie/v1")

type dataBlob struct {
	cbData uint32
	pbData *byte
}

// maxBlobBytes 是交给 DPAPI 的数据长度上限。
// cbData 是 uint32，超长转换会静默截断——那意味着只加密了开头一小段，
// 剩下的悄悄丢掉，用户直到解密时才发现 cookie 是残的。宁可当场拒绝。
const maxBlobBytes = 1 << 20

func newBlob(b []byte) (dataBlob, error) {
	if len(b) == 0 {
		return dataBlob{}, nil
	}
	if len(b) > maxBlobBytes {
		return dataBlob{}, fmt.Errorf("数据长度 %d 超过 %d 字节上限", len(b), maxBlobBytes)
	}
	// #nosec G115 -- 上面刚判过 len(b) <= maxBlobBytes（1MiB），转换不会溢出
	return dataBlob{cbData: uint32(len(b)), pbData: &b[0]}, nil
}

// bytes 把 DPAPI 返回的内存复制成 Go 切片。必须复制：
// 原始内存要交还给 LocalFree，留着引用就是悬垂指针。
func (b dataBlob) bytes() []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

var (
	crypt32              = syscall.NewLazyDLL("crypt32.dll")
	kernel32Local        = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtect     = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect   = crypt32.NewProc("CryptUnprotectData")
	procLocalFreeForBlob = kernel32Local.NewProc("LocalFree")
)

func protectData(plain []byte) ([]byte, error) {
	in, err := newBlob(plain)
	if err != nil {
		return nil, err
	}
	ent, err := newBlob(entropy)
	if err != nil {
		return nil, err
	}
	var out dataBlob

	r, _, callErr := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // 描述串，不需要
		uintptr(unsafe.Pointer(&ent)),
		0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData 失败: %w", callErr)
	}
	defer procLocalFreeForBlob.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

func unprotectData(sealed []byte) ([]byte, error) {
	in, err := newBlob(sealed)
	if err != nil {
		return nil, err
	}
	ent, err := newBlob(entropy)
	if err != nil {
		return nil, err
	}
	var out dataBlob

	r, _, callErr := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // 输出描述串，不需要
		uintptr(unsafe.Pointer(&ent)),
		0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData 失败: %w", callErr)
	}
	defer procLocalFreeForBlob.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}
