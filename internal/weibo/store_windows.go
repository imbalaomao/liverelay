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

func newBlob(b []byte) dataBlob {
	if len(b) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(b)), pbData: &b[0]}
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
	in, ent := newBlob(plain), newBlob(entropy)
	var out dataBlob

	r, _, err := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // 描述串，不需要
		uintptr(unsafe.Pointer(&ent)),
		0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData 失败: %w", err)
	}
	defer procLocalFreeForBlob.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

func unprotectData(sealed []byte) ([]byte, error) {
	in, ent := newBlob(sealed), newBlob(entropy)
	var out dataBlob

	r, _, err := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // 输出描述串，不需要
		uintptr(unsafe.Pointer(&ent)),
		0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData 失败: %w", err)
	}
	defer procLocalFreeForBlob.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}
