package winstate

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tmc/axmcp/internal/computeruse"
)

// Rect is a window rectangle in screen coordinates.
type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// Window describes one top-level Win32 window.
type Window struct {
	Handle      uintptr
	PID         int
	Title       string
	ProcessName string
	Rect        Rect
}

// Backend builds app state from top-level Win32 windows.
type Backend struct {
	windows    func(context.Context) ([]Window, error)
	screenshot func(context.Context, Window) ([]byte, error)
}

var _ computeruse.StateBackend = Backend{}

// NewBackend returns a Windows state backend.
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
			X:       win.Rect.X,
			Y:       win.Rect.Y,
			Width:   win.Rect.Width,
			Height:  win.Rect.Height,
			Enabled: true,
		}},
	}
	if req.Instructions != nil {
		state.Instructions = req.Instructions.Instructions(state.App)
	}
	pngData, err := b.captureScreenshot(ctx, win)
	if err != nil {
		return nil, err
	}
	pngData, cfg, err := computeruse.NormalizeScreenshotPNG(pngData, computeruse.MaxScreenshotLongSide)
	if err != nil {
		return nil, err
	}
	state.ScreenshotPNGBase64 = base64.StdEncoding.EncodeToString(pngData)
	state.Window.ScreenshotWidth = cfg.Width
	state.Window.ScreenshotHeight = cfg.Height
	return &Snapshot{state: state, window: win}, nil
}

func (b Backend) listWindows(ctx context.Context) ([]Window, error) {
	if b.windows != nil {
		return b.windows(ctx)
	}
	return enumerateWindows(ctx)
}

func (b Backend) captureScreenshot(ctx context.Context, win Window) ([]byte, error) {
	if b.screenshot != nil {
		return b.screenshot(ctx, win)
	}
	return captureWindowPNG(ctx, win)
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
		return Window{}, fmt.Errorf("no visible windows found")
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
	return Window{}, fmt.Errorf("no visible window matches %q", selector)
}

// Snapshot is a Windows state snapshot.
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
	return computeruse.WindowInfo{
		WindowID:         uint32(win.Handle),
		Title:            win.Title,
		X:                win.Rect.X,
		Y:                win.Rect.Y,
		Width:            win.Rect.Width,
		Height:           win.Rect.Height,
		ScreenshotWidth:  win.Rect.Width,
		ScreenshotHeight: win.Rect.Height,
	}
}

func exactMatch(win Window, selector string) bool {
	if strconv.Itoa(win.PID) == selector {
		return true
	}
	return strings.EqualFold(win.ProcessName, selector) ||
		strings.EqualFold(win.Title, selector)
}

func containsMatch(win Window, selector string) bool {
	want := strings.ToLower(selector)
	return strings.Contains(strings.ToLower(win.ProcessName), want) ||
		strings.Contains(strings.ToLower(win.Title), want)
}
