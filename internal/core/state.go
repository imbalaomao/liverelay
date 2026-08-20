package core

import "fmt"

type State string

const (
	StateIdle         State = "idle"
	StateQueued       State = "queued"
	StateStarting     State = "starting"
	StateRunning      State = "running"
	StateReconnecting State = "reconnecting"
	StateFailed       State = "failed"
	StateMonitoring   State = "monitoring"
)

var transitions = map[State][]State{
	StateIdle:         {StateStarting, StateQueued, StateMonitoring},
	StateQueued:       {StateStarting, StateIdle},
	StateStarting:     {StateRunning, StateFailed, StateIdle},
	StateRunning:      {StateReconnecting, StateIdle, StateFailed},
	StateReconnecting: {StateStarting, StateRunning, StateFailed, StateIdle},
	StateFailed:       {StateIdle, StateStarting, StateMonitoring},
	StateMonitoring:   {StateStarting, StateIdle},
}

func CanTransition(from, to State) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

func Transition(from, to State) (State, error) {
	if !CanTransition(from, to) {
		return from, fmt.Errorf("非法状态迁移: %s → %s", from, to)
	}
	return to, nil
}
