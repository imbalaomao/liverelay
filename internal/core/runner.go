package core

import (
	"context"

	"github.com/imbalaomao/liverelay/internal/config"
)

// ExitInfo 描述 Runner 结束的原因。
type ExitInfo struct {
	Normal bool
	Err    error
}

// Runner 封装一条直播管道的生命周期。
type Runner interface {
	Start(ctx context.Context) error
	Wait() ExitInfo
	Stop() error
}

// RunnerFactory 根据任务配置构造 Runner。
type RunnerFactory func(t config.Task) Runner
