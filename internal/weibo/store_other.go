//go:build !windows

package weibo

import "errors"

// storageScope 在非 Windows 上标成 plain：本机没有 DPAPI 这样的
// 与系统账户绑定的保护机制，与其假装加密，不如如实标出来。
const storageScope = "plain"

// 本项目只面向 Windows。这里留实现是为了让整个仓库能在其它平台跑通测试，
// 但拒绝真的以明文存储凭据——那比不支持更糟。
func protectData([]byte) ([]byte, error) {
	return nil, errors.New("当前平台没有可用的凭据保护机制，拒绝以明文保存微博 cookie")
}

func unprotectData([]byte) ([]byte, error) {
	return nil, errors.New("当前平台没有可用的凭据保护机制")
}
