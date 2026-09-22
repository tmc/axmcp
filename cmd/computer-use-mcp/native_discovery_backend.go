package main

import (
	"context"
	"fmt"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/session"
	"github.com/tmc/axmcp/internal/purego/cfhandle"
)

func (b *nativeOSBackend) DiscoveryAccess(ctx context.Context, app computeruse.AppInfo) (computeruse.PermissionState, computeruse.ApprovalState, error) {
	permissions := currentPermissions()
	if b.rt == nil || b.rt.approvals == nil {
		return permissions, computeruse.ApprovalState{}, fmt.Errorf("app approval store unavailable")
	}
	return permissions, b.rt.approvals.Status(app.BundleID), ctx.Err()
}

// The group keeps the parent Application alive while its candidate Elements can
// refer to it. Capturing opens a separate Application and separate window handles.
type nativeOSWindowSet struct {
	backend   *nativeOSBackend
	app       *axuiautomation.Application
	windows   []*axuiautomation.Element
	targets   []nativeTarget
	start     nativeStart
	truncated bool
}

func (b *nativeOSBackend) Discover(ctx context.Context, info computeruse.AppInfo, limit int) (nativeWindowSet, error) {
	start, err := nativeProcessStart(info.PID)
	if err != nil {
		return nil, err
	}
	app := axuiautomation.NewApplicationFromPID(int32(info.PID))
	if app == nil {
		return nil, fmt.Errorf("cannot connect to pid %d", info.PID)
	}
	group := &nativeOSWindowSet{backend: b, app: app, start: start}
	keep := false
	defer func() {
		if !keep {
			group.Close()
		}
	}()
	if err := nativeAXBudget(ctx, app.Root()); err != nil {
		return nil, err
	}
	windows := app.WindowList()
	// Own every result immediately, including handles excluded by the limit.
	group.windows = windows
	group.truncated = len(windows) > limit
	targets := make([]nativeTarget, 0, len(windows))
	for i, window := range windows {
		if i >= limit {
			if window != nil {
				window.Release()
			}
			windows[i] = nil
			continue
		}
		if window == nil {
			continue
		}
		if err := nativeAXBudget(ctx, window); err != nil {
			return nil, err
		}
		metadata, ok := readNativeWindow(window)
		if !ok || metadata.WindowID == 0 {
			window.Release()
			windows[i] = nil
			continue
		}
		if err := nativeAXBudget(ctx, window); err != nil {
			return nil, err
		}
		metadata.Title = window.Title()
		targets = append(targets, nativeTarget{App: info, Start: start, Window: metadata})
	}
	compact := make([]*axuiautomation.Element, 0, len(targets))
	for _, window := range windows {
		if window != nil {
			compact = append(compact, window)
		}
	}
	group.windows, group.targets = compact, targets
	after, err := nativeProcessStart(info.PID)
	if err != nil {
		return nil, err
	}
	if after != start {
		return nil, fmt.Errorf("process changed during discovery")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keep = true
	return group, nil
}
func (g *nativeOSWindowSet) Targets() []nativeTarget {
	return append([]nativeTarget(nil), g.targets...)
}
func (g *nativeOSWindowSet) ProcessStart() nativeStart { return g.start }
func (g *nativeOSWindowSet) Truncated() bool           { return g.truncated }
func (g *nativeOSWindowSet) Capture(ctx context.Context, index int) (session.Snapshot, nativeTarget, error) {
	if g.app == nil || index < 0 || index >= len(g.windows) {
		return nil, nativeTarget{}, fmt.Errorf("discovery window unavailable")
	}
	target := g.targets[index]
	return g.backend.capture(ctx, target.App, target.Window.WindowID, g.windows[index], &g.start)
}
func (g *nativeOSWindowSet) Close() error {
	for _, window := range g.windows {
		if window != nil {
			window.Release()
		}
	}
	g.windows = nil
	g.targets = nil
	if g.app != nil {
		g.app.Close()
		g.app = nil
	}
	return nil
}

func (g *nativeOSWindowSet) Validate(ctx context.Context, index int) (nativeTarget, error) {
	if g.app == nil || index < 0 || index >= len(g.windows) {
		return nativeTarget{}, fmt.Errorf("discovery window unavailable")
	}
	lib, err := cfhandle.Open()
	if err != nil {
		return nativeTarget{}, err
	}
	target := g.targets[index]
	before, err := nativeProcessStart(target.App.PID)
	if err != nil {
		return nativeTarget{}, err
	}
	if before != g.start {
		return nativeTarget{}, fmt.Errorf("process changed since discovery")
	}
	window := g.windows[index]
	if err := nativeAXBudget(ctx, window); err != nil {
		return nativeTarget{}, err
	}
	metadata, ok := readNativeWindow(window)
	if !ok || metadata.WindowID != target.Window.WindowID {
		return nativeTarget{}, fmt.Errorf("native selection instance changed")
	}
	if err := nativeAXBudget(ctx, g.app.Root()); err != nil {
		return nativeTarget{}, err
	}
	matches := 0
	for _, current := range g.app.WindowList() {
		if current == nil {
			continue
		}
		if lib.Equal(current.Ref(), window.Ref()) {
			matches++
		}
		current.Release()
	}
	if matches != 1 {
		return nativeTarget{}, fmt.Errorf("native window reference unavailable or ambiguous")
	}
	if err := nativeAXBudget(ctx, window); err != nil {
		return nativeTarget{}, err
	}
	metadata.Title = window.Title()
	after, err := nativeProcessStart(target.App.PID)
	if err != nil {
		return nativeTarget{}, err
	}
	if before != after {
		return nativeTarget{}, fmt.Errorf("process changed during selection")
	}
	if err := ctx.Err(); err != nil {
		return nativeTarget{}, err
	}
	target.Window = metadata
	return target, nil
}
