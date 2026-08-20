//go:build !windows

package power

// 非 Windows 平台没有对应的系统能力。留个空实现，
// 是为了让 go vet / go test 在其它平台上也能跑通整个仓库。
func supported() bool { return false }

func setKeepAwake(bool) error { return nil }
