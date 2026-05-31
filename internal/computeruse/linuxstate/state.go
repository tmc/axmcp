package linuxstate

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/tmc/axmcp/internal/computeruse"
)

// Window describes one X11 top-level window.
type Window struct {
	ID          string
	Desktop     string
	PID         int
	Title       string
	ProcessName string
	X           int
	Y           int
	Width       int
	Height      int
}

// Backend builds app state from X11 top-level windows.
type Backend struct {
	run func(context.Context, string, ...string) ([]byte, error)
}

var _ computeruse.StateBackend = Backend{}

// NewBackend returns a Linux state backend.
func NewBackend() Backend {
	return Backend{}
}

func (b Backend) ListApps(ctx context.Context) ([]computeruse.AppInfo, error) {
	windows, err := b.listWindows(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[int]bool)
	var apps []computeruse.AppInfo
	for _, win := range windows {
		if win.PID <= 0 || seen[win.PID] {
			continue
		}
		seen[win.PID] = true
		apps = append(apps, appInfo(win))
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Name != apps[j].Name {
			return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
		}
		return apps[i].PID < apps[j].PID
	})
	return apps, nil
}

func (b Backend) ResolveApp(ctx context.Context, selector string) (computeruse.AppInfo, error) {
	win, err := b.resolveWindow(ctx, selector)
	if err != nil {
		return computeruse.AppInfo{}, err
	}
	return appInfo(win), nil
}

func (b Backend) BuildState(ctx context.Context, req computeruse.StateRequest) (computeruse.Snapshot, error) {
	win, err := b.resolveWindow(ctx, req.App)
	if err != nil {
		return nil, err
	}
	state := computeruse.AppState{
		App:    appInfo(win),
		Window: windowInfo(win),
		Tree: []computeruse.ElementNode{{
			Index:   0,
			Role:    "Window",
			Title:   win.Title,
			X:       win.X,
			Y:       win.Y,
			Width:   win.Width,
			Height:  win.Height,
			Enabled: true,
		}},
	}
	if req.Instructions != nil {
		state.Instructions = req.Instructions.Instructions(state.App)
	}
	return &Snapshot{state: state, window: win}, nil
}

func (b Backend) listWindows(ctx context.Context) ([]Window, error) {
	run := b.run
	if run == nil {
		run = runCommand
	}
	out, err := run(ctx, "wmctrl", "-lpG")
	if err != nil {
		return nil, fmt.Errorf("list X11 windows with wmctrl: %w", err)
	}
	return parseWMCTRL(out)
}

func (b Backend) resolveWindow(ctx context.Context, selector string) (Window, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return Window{}, fmt.Errorf("missing app selector")
	}
	windows, err := b.listWindows(ctx)
	if err != nil {
		return Window{}, err
	}
	if len(windows) == 0 {
		return Window{}, fmt.Errorf("no X11 windows found")
	}
	for _, win := range windows {
		if exactMatch(win, selector) {
			return win, nil
		}
	}
	for _, win := range windows {
		if containsMatch(win, selector) {
			return win, nil
		}
	}
	return Window{}, fmt.Errorf("no X11 window matches %q", selector)
}

// Snapshot is a Linux state snapshot.
type Snapshot struct {
	state  computeruse.AppState
	window Window
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

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func appInfo(win Window) computeruse.AppInfo {
	name := strings.TrimSpace(win.ProcessName)
	if name == "" {
		name = strings.TrimSpace(win.Title)
	}
	return computeruse.AppInfo{
		Name: name,
		PID:  win.PID,
	}
}

func windowInfo(win Window) computeruse.WindowInfo {
	id, _ := strconv.ParseUint(strings.TrimPrefix(win.ID, "0x"), 16, 32)
	return computeruse.WindowInfo{
		WindowID:         uint32(id),
		Title:            win.Title,
		X:                win.X,
		Y:                win.Y,
		Width:            win.Width,
		Height:           win.Height,
		ScreenshotWidth:  win.Width,
		ScreenshotHeight: win.Height,
	}
}

func exactMatch(win Window, selector string) bool {
	if strconv.Itoa(win.PID) == selector {
		return true
	}
	return strings.EqualFold(win.ProcessName, selector) ||
		strings.EqualFold(win.Title, selector) ||
		strings.EqualFold(win.ID, selector)
}

func containsMatch(win Window, selector string) bool {
	want := strings.ToLower(selector)
	return strings.Contains(strings.ToLower(win.ProcessName), want) ||
		strings.Contains(strings.ToLower(win.Title), want)
}
