package core

import (
	"testing"
	"time"
)

// 稳定运行后再断流，应从 Min 重新起步，而不是延用上次已升高的退避值。
func TestBackoffReset(t *testing.T) {
	b := &Backoff{Min: time.Second, Max: 60 * time.Second}
	b.Next()
	b.Next()
	if d := b.Next(); d != 4*time.Second {
		t.Fatalf("第三次退避应为 4s，得到 %v", d)
	}
	b.Reset()
	if d := b.Next(); d != time.Second {
		t.Fatalf("Reset 后应回到 Min=1s，得到 %v", d)
	}
}

// Min 非正时必须有下限，否则 Next() 恒返回 0，重连变成零延迟紧循环，
// 直接违反"不侵占系统过多资源"红线。
func TestBackoffNonPositiveMinHasFloor(t *testing.T) {
	for _, min := range []time.Duration{0, -time.Second} {
		b := &Backoff{Min: min, Max: 60 * time.Second}
		if d := b.Next(); d <= 0 {
			t.Fatalf("Min=%v 时退避为 %v，会造成紧循环重连", min, d)
		}
	}
}

// Max 非正时不应把退避钳死为 0。
func TestBackoffNonPositiveMaxFallsBackToMin(t *testing.T) {
	b := &Backoff{Min: 2 * time.Second}
	if d := b.Next(); d < 2*time.Second {
		t.Fatalf("Max 未设置时首次退避应 >= Min，得到 %v", d)
	}
}
