//go:build !darwin

package main

import (
	"fmt"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/instruction"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

type runtimeState struct {
	backend      computeruse.Backend
	instructions *instruction.Provider
	recording    *trajectoryRecorder
	sessions     *session.Store
}

type runtimeOptions struct{}

func newRuntimeState(...runtimeOptions) (*runtimeState, error) {
	return &runtimeState{
		backend:      newNonDarwinBackend(),
		instructions: instruction.New(),
		recording:    newTrajectoryRecorder(),
		sessions:     session.NewStore(),
	}, nil
}

func (rt *runtimeState) stateBackend() computeruse.StateBackend {
	if rt != nil && rt.backend != nil && rt.backend.State() != nil {
		return rt.backend.State()
	}
	return computeruse.NewUnsupportedBackend(computeruse.PlatformStatus()).State()
}

func (rt *runtimeState) inputBackend() computeruse.InputBackend {
	if rt != nil && rt.backend != nil && rt.backend.Input() != nil {
		return rt.backend.Input()
	}
	return computeruse.NewUnsupportedBackend(computeruse.PlatformStatus()).Input()
}

func (rt *runtimeState) bindSnapshot(snapshot computeruse.Snapshot) (computeruse.AppState, error) {
	if rt == nil || rt.sessions == nil {
		if snapshot != nil {
			_ = snapshot.Close()
		}
		return computeruse.AppState{}, fmt.Errorf("runtime is missing session store")
	}
	state, err := rt.sessions.Bind(snapshot)
	if err != nil {
		if snapshot != nil {
			_ = snapshot.Close()
		}
		return computeruse.AppState{}, err
	}
	return state, nil
}

func (rt *runtimeState) snapshotForAction(stateID string) (computeruse.Snapshot, error) {
	if rt == nil || rt.sessions == nil {
		return nil, fmt.Errorf("runtime is missing session store")
	}
	return rt.sessions.Snapshot(stateID)
}
