package core

import (
	"testing"
	"time"
)

func TestTransitionTable(t *testing.T) {
	valid := [][2]State{
		{StateIdle, StateStarting}, {StateIdle, StateQueued}, {StateIdle, StateMonitoring},
		{StateQueued, StateStarting}, {StateQueued, StateIdle},
		{StateStarting, StateRunning}, {StateStarting, StateFailed}, {StateStarting, StateIdle},
		{StateRunning, StateReconnecting}, {StateRunning, StateIdle}, {StateRunning, StateFailed},
		{StateReconnecting, StateStarting}, {StateReconnecting, StateRunning},
		{StateReconnecting, StateFailed}, {StateReconnecting, StateIdle},
		{StateFailed, StateIdle}, {StateFailed, StateStarting}, {StateFailed, StateMonitoring},
		{StateMonitoring, StateStarting}, {StateMonitoring, StateIdle},
	}
	for _, pair := range valid {
		from, to := pair[0], pair[1]
		if !CanTransition(from, to) {
			t.Errorf("应为合法迁移: %s → %s", from, to)
		}
	}
	invalid := [][2]State{
		{StateIdle, StateRunning}, {StateRunning, StateStarting},
		{StateQueued, StateRunning}, {StateFailed, StateRunning},
	}
	for _, pair := range invalid {
		from, to := pair[0], pair[1]
		if CanTransition(from, to) {
			t.Errorf("应为非法迁移: %s → %s", from, to)
		}
	}
}

func TestBackoffProgression(t *testing.T) {
	b := &Backoff{Min: 2 * time.Second, Max: 60 * time.Second}
	want := []time.Duration{2, 4, 8, 16, 32, 60, 60}
	for i, w := range want {
		if got := b.Next(); got != w*time.Second {
			t.Fatalf("第 %d 次退避 = %v, 期望 %v", i, got, w*time.Second)
		}
	}
}
