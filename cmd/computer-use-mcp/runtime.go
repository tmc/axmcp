//go:build darwin

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/approval"
	"github.com/tmc/axmcp/internal/computeruse/appstate"
	"github.com/tmc/axmcp/internal/computeruse/instruction"
	"github.com/tmc/axmcp/internal/computeruse/intervention"
	"github.com/tmc/axmcp/internal/computeruse/policy"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

type runtimeState struct {
	approvals    *approval.Store
	backend      computeruse.Backend
	builder      *appstate.Builder
	instructions *instruction.Provider
	intervention *intervention.Monitor
	recording    *trajectoryRecorder
	urlPolicy    *policy.URLPolicy
	sessions     *session.Store
}

type runtimeOptions struct {
	intervention intervention.Config
	blockedURLs  []string
}

func newRuntimeState(opts ...runtimeOptions) (*runtimeState, error) {
	approvals, err := approval.New()
	if path := os.Getenv("COMPUTER_USE_MCP_APPROVALS_PATH"); path != "" {
		approvals, err = approval.Open(path)
	}
	if err != nil {
		return nil, fmt.Errorf("approval store: %w", err)
	}
	var opt runtimeOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.intervention.QuietPeriod <= 0 {
		opt.intervention.QuietPeriod = 750 * time.Millisecond
	}
	monitor := intervention.New(opt.intervention)
	if err := monitor.Start(); err != nil {
		return nil, fmt.Errorf("human intervention monitor: %w", err)
	}
	builder := appstate.NewBuilder()
	return &runtimeState{
		approvals:    approvals,
		backend:      newDarwinBackend(builder, monitor),
		builder:      builder,
		instructions: instruction.New(),
		intervention: monitor,
		recording:    newTrajectoryRecorder(),
		urlPolicy:    policy.NewURLPolicy(opt.blockedURLs),
		sessions:     session.NewStore(),
	}, nil
}

func (rt *runtimeState) stateBackend() computeruse.StateBackend {
	if rt != nil && rt.backend != nil && rt.backend.State() != nil {
		return rt.backend.State()
	}
	if rt != nil && rt.builder != nil {
		return (&darwinStateBackend{builder: rt.builder})
	}
	return newDarwinBackend(nil, nil).State()
}

func (rt *runtimeState) bindSnapshot(snapshot computeruse.Snapshot) (computeruse.AppState, error) {
	if rt == nil || rt.sessions == nil {
		if snapshot != nil {
			_ = snapshot.Close()
		}
		return computeruse.AppState{}, fmt.Errorf("runtime is missing session store")
	}
	actionSnapshot, err := requireActionSnapshot(snapshot)
	if err != nil {
		if snapshot != nil {
			_ = snapshot.Close()
		}
		return computeruse.AppState{}, err
	}
	state, err := rt.sessions.Bind(actionSnapshot)
	if err != nil {
		_ = actionSnapshot.Close()
		return computeruse.AppState{}, err
	}
	return state, nil
}
