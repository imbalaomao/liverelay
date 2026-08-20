package core

import "time"

// backoffFloor 是退避下限。Backoff 是导出类型，零值 Backoff{} 若不设下限会让
// Next() 恒返回 0，重连退化为零延迟紧循环——直接违反"不侵占系统过多资源"红线。
const backoffFloor = time.Second

// Backoff 是指数退避计时器（规格 §4.4：2s 起步，封顶 60s）。
// 非并发安全：每个 supervise goroutine 独占一个实例。
type Backoff struct {
	Min, Max time.Duration
	attempt  int
}

func (b *Backoff) Next() time.Duration {
	min := b.Min
	if min <= 0 {
		min = backoffFloor
	}
	max := b.Max
	if max < min {
		max = min
	}
	d := min
	for i := 0; i < b.attempt; i++ {
		d *= 2
		if d >= max {
			d = max
			break
		}
	}
	b.attempt++
	return d
}

// Reset 让下一次 Next() 回到 Min。稳定推流一段时间后再断流应重新起步，
// 而不是延用上一轮已升高的退避值。
func (b *Backoff) Reset() { b.attempt = 0 }
