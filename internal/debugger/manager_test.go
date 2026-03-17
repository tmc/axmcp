package debugger

import (
	"context"
	"fmt"
	"testing"

	"github.com/tmc/xcmcp/internal/macosapp"
)

type fakeProcess struct {
	commands []string
	outputs  map[string]string
	closed   bool
}

func (p *fakeProcess) Run(_ context.Context, command string) (string, error) {
	p.commands = append(p.commands, command)
	if out, ok := p.outputs[command]; ok {
		return out, nil
	}
	return "", nil
}

func (p *fakeProcess) Close() error {
	p.closed = true
	return nil
}

func TestBreakpointSetCommand(t *testing.T) {
	tests := []struct {
		name string
		spec BreakpointSpec
		want string
		err  string
	}{
		{
			name: "raw",
			spec: BreakpointSpec{Raw: "--name main"},
			want: "breakpoint set --name main",
		},
		{
			name: "name",
			spec: BreakpointSpec{Name: "main"},
			want: `breakpoint set --name "main"`,
		},
		{
			name: "file line",
			spec: BreakpointSpec{File: "/tmp/App.swift", Line: 42},
			want: `breakpoint set --file "/tmp/App.swift" --line 42`,
		},
		{
			name: "address",
			spec: BreakpointSpec{Address: "0x1000"},
			want: "breakpoint set --address 0x1000",
		},
		{
			name: "missing target",
			spec: BreakpointSpec{},
			err:  "breakpoint target is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := breakpointSetCommand(tt.spec)
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("breakpointSetCommand(%+v) error = %v, want %q", tt.spec, err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("breakpointSetCommand(%+v): %v", tt.spec, err)
			}
			if got != tt.want {
				t.Fatalf("breakpointSetCommand(%+v) = %q, want %q", tt.spec, got, tt.want)
			}
		})
	}
}

func TestManagerAttachRunDetach(t *testing.T) {
	proc := &fakeProcess{
		outputs: map[string]string{
			"thread backtrace":             "stack",
			`breakpoint set --name "main"`: "breakpoint 1",
			"breakpoint delete 1":          "deleted",
			"process continue &":           "continued",
			"process detach":               "detached",
			`expression -- "hello"`:        "hello",
			"frame variable":               "vars",
		},
	}
	manager := NewManager()
	manager.resolve = func(context.Context, Selector) (*macosapp.RunningApp, error) {
		return &macosapp.RunningApp{
			Name:     "Mesh",
			BundleID: "dev.tmc.Mesh",
			PID:      42,
		}, nil
	}
	manager.newProc = func(context.Context, int) (process, string, error) {
		return proc, "attached", nil
	}

	info, output, err := manager.Attach(context.Background(), Selector{Name: "Mesh"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if output != "attached" {
		t.Fatalf("Attach output = %q, want attached", output)
	}
	if info.ID != "dbg-0001" {
		t.Fatalf("Attach session ID = %q, want dbg-0001", info.ID)
	}
	if info.PID != 42 {
		t.Fatalf("Attach PID = %d, want 42", info.PID)
	}

	sessions := manager.List()
	if len(sessions) != 1 {
		t.Fatalf("len(List()) = %d, want 1", len(sessions))
	}

	stack, err := manager.Stack(context.Background(), info.ID)
	if err != nil {
		t.Fatalf("Stack: %v", err)
	}
	if stack != "stack" {
		t.Fatalf("Stack output = %q, want stack", stack)
	}

	if _, err := manager.AddBreakpoint(context.Background(), info.ID, BreakpointSpec{Name: "main"}); err != nil {
		t.Fatalf("AddBreakpoint: %v", err)
	}
	if _, err := manager.RemoveBreakpoint(context.Background(), info.ID, 1); err != nil {
		t.Fatalf("RemoveBreakpoint: %v", err)
	}
	if _, err := manager.Continue(context.Background(), info.ID); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if _, err := manager.Run(context.Background(), info.ID, `expression -- "hello"`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := manager.Variables(context.Background(), info.ID); err != nil {
		t.Fatalf("Variables: %v", err)
	}

	detach, err := manager.Detach(context.Background(), info.ID)
	if err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if detach != "detached" {
		t.Fatalf("Detach output = %q, want detached", detach)
	}
	if !proc.closed {
		t.Fatal("Detach did not close the process")
	}
	if got := manager.List(); len(got) != 0 {
		t.Fatalf("len(List()) after detach = %d, want 0", len(got))
	}

	wantCommands := []string{
		"thread backtrace",
		`breakpoint set --name "main"`,
		"breakpoint delete 1",
		"process continue &",
		`expression -- "hello"`,
		"frame variable",
		"process detach",
	}
	if fmt.Sprint(proc.commands) != fmt.Sprint(wantCommands) {
		t.Fatalf("commands = %v, want %v", proc.commands, wantCommands)
	}
}

func TestManagerAttachRequiresSelector(t *testing.T) {
	manager := NewManager()
	if _, _, err := manager.Attach(context.Background(), Selector{}); err == nil {
		t.Fatal("Attach with empty selector succeeded, want error")
	}
}

func TestBootstrapCommands(t *testing.T) {
	got := bootstrapCommands(42)
	want := []string{
		"settings set auto-confirm true",
		"settings set use-color false",
		"process attach --pid 42",
		"thread list",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("bootstrapCommands = %v, want %v", got, want)
	}
}
