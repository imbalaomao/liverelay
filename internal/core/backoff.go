package core

import "time"

type Backoff struct {
	Min, Max time.Duration
	attempt  int
}

func (b *Backoff) Next() time.Duration {
	d := b.Min
	for i := 0; i < b.attempt; i++ {
		d *= 2
		if d >= b.Max {
			d = b.Max
			break
		}
	}
	b.attempt++
	return d
}

func (b *Backoff) Reset() { b.attempt = 0 }
