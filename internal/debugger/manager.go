package debugger

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tmc/axmcp/internal/macosapp"
)

type Selector struct {
	BundleID string
	Name     string
	PID      int
}

type SessionInfo struct {
	ID       string `json:"id"`
	PID      int    `json:"pid"`
	BundleID string `json:"bundle_id,omitempty"`
	Name     string `json:"name,omitempty"`
}

type Manager struct {
	mu       sync.Mutex
	nextID   atomic.Int64
	sessions map[string]*session
	newProc  func(context.Context, int) (process, string, error)
	resolve  func(context.Context, Selector) (*macosapp.RunningApp, error)
}

type session struct {
	info SessionInfo
	proc process
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*session),
		newProc: func(ctx context.Context, pid int) (process, string, error) {
			return newLLDBProcess(ctx, pid)
		},
		resolve: resolveApp,
	}
}

func (m *Manager) Attach(ctx context.Context, sel Selector) (SessionInfo, string, error) {
	if sel.BundleID == "" && sel.Name == "" && sel.PID == 0 {
		return SessionInfo{}, "", fmt.Errorf("bundle_id, name, or pid is required")
	}
	app, err := m.resolve(ctx, sel)
	if err != nil {
		return SessionInfo{}, "", err
	}

	proc, output, err := m.newProc(ctx, app.PID)
	if err != nil {
		return SessionInfo{}, output, err
	}
	info := SessionInfo{
		ID:       fmt.Sprintf("dbg-%04d", m.nextID.Add(1)),
		PID:      app.PID,
		BundleID: app.BundleID,
		Name:     app.Name,
	}
	m.mu.Lock()
	m.sessions[info.ID] = &session{info: info, proc: proc}
	m.mu.Unlock()
	return info, output, nil
}

func (m *Manager) List() []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.info)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *Manager) Run(ctx context.Context, id, command string) (string, error) {
	s, err := m.session(id)
	if err != nil {
		return "", err
	}
	return s.proc.Run(ctx, command)
}

func (m *Manager) Continue(ctx context.Context, id string) (string, error) {
	return m.Run(ctx, id, "process continue &")
}

func (m *Manager) Stack(ctx context.Context, id string) (string, error) {
	return m.Run(ctx, id, "thread backtrace")
}

func (m *Manager) Variables(ctx context.Context, id string) (string, error) {
	return m.Run(ctx, id, "frame variable")
}

func (m *Manager) AddBreakpoint(ctx context.Context, id string, spec BreakpointSpec) (string, error) {
	command, err := breakpointSetCommand(spec)
	if err != nil {
		return "", err
	}
	return m.Run(ctx, id, command)
}

func (m *Manager) RemoveBreakpoint(ctx context.Context, id string, breakpointID int) (string, error) {
	return m.Run(ctx, id, fmt.Sprintf("breakpoint delete %d", breakpointID))
}

func (m *Manager) Detach(ctx context.Context, id string) (string, error) {
	s, err := m.session(id)
	if err != nil {
		return "", err
	}
	out, runErr := s.proc.Run(ctx, "process detach")
	closeErr := s.proc.Close()

	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()

	if runErr != nil {
		return out, runErr
	}
	return out, closeErr
}

func (m *Manager) session(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return nil, fmt.Errorf("unknown debug session %q", id)
	}
	return s, nil
}

type BreakpointSpec struct {
	File    string
	Line    int
	Name    string
	Raw     string
	Address string
}

func breakpointSetCommand(spec BreakpointSpec) (string, error) {
	switch {
	case spec.Raw != "":
		return "breakpoint set " + strings.TrimSpace(spec.Raw), nil
	case spec.Name != "":
		return "breakpoint set --name " + shellQuote(spec.Name), nil
	case spec.File != "" && spec.Line > 0:
		return fmt.Sprintf("breakpoint set --file %s --line %d", shellQuote(spec.File), spec.Line), nil
	case spec.Address != "":
		return "breakpoint set --address " + strings.TrimSpace(spec.Address), nil
	default:
		return "", fmt.Errorf("breakpoint target is required")
	}
}

func shellQuote(s string) string {
	return fmt.Sprintf("%q", s)
}

func resolveApp(ctx context.Context, sel Selector) (*macosapp.RunningApp, error) {
	waitCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	return macosapp.WaitForProcess(waitCtx, macosapp.AppSelector{
		BundleID: sel.BundleID,
		Name:     sel.Name,
		PID:      sel.PID,
	})
}
