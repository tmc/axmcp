//go:build !darwin

package main

import (
	"runtime"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/linuxstate"
	"github.com/tmc/axmcp/internal/computeruse/winstate"
)

type nonDarwinBackend struct {
	state       computeruse.StateBackend
	unsupported *computeruse.UnsupportedBackend
}

func newNonDarwinBackend() computeruse.Backend {
	report := computeruse.PlatformStatus()
	unsupported := computeruse.NewUnsupportedBackend(report)
	var state computeruse.StateBackend
	switch runtime.GOOS {
	case "windows":
		state = winstate.NewBackend()
	case "linux":
		state = linuxstate.NewBackend()
	default:
		state = unsupported.State()
	}
	return &nonDarwinBackend{
		state:       state,
		unsupported: unsupported,
	}
}

func (b *nonDarwinBackend) Platform() computeruse.PlatformReport {
	return computeruse.PlatformStatus()
}

func (b *nonDarwinBackend) State() computeruse.StateBackend {
	return b.state
}

func (b *nonDarwinBackend) Input() computeruse.InputBackend {
	return b.unsupported.Input()
}

func (b *nonDarwinBackend) Screenshots() computeruse.ScreenshotBackend {
	return b.unsupported.Screenshots()
}

func (b *nonDarwinBackend) Intervention() computeruse.InterventionBackend {
	return b.unsupported.Intervention()
}
