//go:build windows

package power

import (
	"fmt"
	"syscall"
)

// SetThreadExecutionState 的标志位。
// 只用 ES_SYSTEM_REQUIRED，不加 ES_DISPLAY_REQUIRED：用户要的是"电脑别睡着"，
// 顺手把屏幕也一直点亮既费电又刺眼，尤其是通宵挂机录制的场景。
const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002 //nolint:unused // 备查：有需要时可加上
)

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
)

func supported() bool { return true }

// setKeepAwake 必须在锁定的 OS 线程上调用——执行状态是挂在线程上的。
func setKeepAwake(keepAwake bool) error {
	flags := uintptr(esContinuous)
	if keepAwake {
		flags |= esSystemRequired
	}
	// 返回值是调用前的状态；返回 0 表示失败
	r, _, err := procSetThreadExecutionState.Call(flags)
	if r == 0 {
		if err != nil && err != syscall.Errno(0) {
			return fmt.Errorf("SetThreadExecutionState 失败: %w", err)
		}
		return fmt.Errorf("SetThreadExecutionState 失败")
	}
	return nil
}
