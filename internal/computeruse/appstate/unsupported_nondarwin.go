//go:build !darwin

package appstate

import (
	"context"

	"github.com/tmc/axmcp/internal/computeruse"
)

type Builder struct{}

type Snapshot struct {
	state computeruse.AppState
}

func NewBuilder() *Builder {
	return &Builder{}
}

func ListApps(context.Context) ([]computeruse.AppInfo, error) {
	return nil, computeruse.PlatformUnsupported("list apps")
}

func ResolveApp(context.Context, string) (computeruse.AppInfo, error) {
	return computeruse.AppInfo{}, computeruse.PlatformUnsupported("resolve app")
}

func (b *Builder) Build(context.Context, string, string, computeruse.InstructionProvider) (*Snapshot, error) {
	return nil, computeruse.PlatformUnsupported("build app state")
}

func (s *Snapshot) State() computeruse.AppState {
	if s == nil {
		return computeruse.AppState{}
	}
	return s.state
}

func (s *Snapshot) Close() error {
	return nil
}
