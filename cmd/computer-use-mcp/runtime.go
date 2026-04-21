package main

import (
	"fmt"

	"github.com/tmc/axmcp/internal/computeruse/approval"
	"github.com/tmc/axmcp/internal/computeruse/appstate"
	"github.com/tmc/axmcp/internal/computeruse/instruction"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

type runtimeState struct {
	approvals    *approval.Store
	builder      *appstate.Builder
	instructions *instruction.Provider
	sessions     *session.Store
}

func newRuntimeState() (*runtimeState, error) {
	approvals, err := approval.New()
	if err != nil {
		return nil, fmt.Errorf("approval store: %w", err)
	}
	return &runtimeState{
		approvals:    approvals,
		builder:      appstate.NewBuilder(),
		instructions: instruction.New(),
		sessions:     session.NewStore(),
	}, nil
}
