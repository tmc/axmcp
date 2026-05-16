package main

import (
	"encoding/json"
	"sync"
	"time"
)

type trajectoryRecorder struct {
	mu        sync.Mutex
	enabled   bool
	replaying bool
	steps     []trajectoryStep
}

type trajectoryStep struct {
	Index     int            `json:"index"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
	Result    any            `json:"result,omitempty"`
	CreatedAt string         `json:"created_at"`
}

type setRecordingInput struct {
	Enabled bool `json:"enabled"`
	Clear   bool `json:"clear,omitempty"`
}

type setRecordingOutput struct {
	Enabled bool `json:"enabled"`
	Count   int  `json:"count"`
}

type replayTrajectoryInput struct {
	FromStep int  `json:"from_step,omitempty"`
	DryRun   bool `json:"dry_run,omitempty"`
}

type replayTrajectoryOutput struct {
	Replayed int              `json:"replayed"`
	Steps    []trajectoryStep `json:"steps"`
}

func newTrajectoryRecorder() *trajectoryRecorder {
	return &trajectoryRecorder{}
}

func (r *trajectoryRecorder) set(enabled, clear bool) setRecordingOutput {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = enabled
	if clear {
		r.steps = nil
	}
	return setRecordingOutput{Enabled: r.enabled, Count: len(r.steps)}
}

func (r *trajectoryRecorder) snapshot(fromStep int) []trajectoryStep {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []trajectoryStep
	for _, step := range r.steps {
		if step.Index >= fromStep {
			out = append(out, cloneStep(step))
		}
	}
	return out
}

func (r *trajectoryRecorder) record(tool string, args, result any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled || r.replaying {
		return
	}
	step := trajectoryStep{
		Index:     len(r.steps) + 1,
		Tool:      tool,
		Args:      toMap(args),
		Result:    result,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	delete(step.Args, "state_id")
	r.steps = append(r.steps, step)
}

func (r *trajectoryRecorder) replayingMode(fn func() error) error {
	r.mu.Lock()
	r.replaying = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.replaying = false
		r.mu.Unlock()
	}()
	return fn()
}

func cloneStep(step trajectoryStep) trajectoryStep {
	step.Args = cloneMap(step.Args)
	return step
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func toMap(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}
