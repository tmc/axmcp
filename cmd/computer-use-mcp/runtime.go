//go:build darwin

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/tmc/axmcp/internal/computeruse/approval"
	"github.com/tmc/axmcp/internal/computeruse/appstate"
	"github.com/tmc/axmcp/internal/computeruse/instruction"
	"github.com/tmc/axmcp/internal/computeruse/intervention"
	"github.com/tmc/axmcp/internal/computeruse/policy"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

type runtimeState struct {
	approvals    *approval.Store
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
	return &runtimeState{
		approvals:    approvals,
		builder:      appstate.NewBuilder(),
		instructions: instruction.New(),
		intervention: monitor,
		recording:    newTrajectoryRecorder(),
		urlPolicy:    policy.NewURLPolicy(opt.blockedURLs),
		sessions:     session.NewStore(),
	}, nil
}
