//go:build darwin

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/appstate"
	"github.com/tmc/axmcp/internal/computeruse/coords"
	"github.com/tmc/axmcp/internal/computeruse/input"
	"github.com/tmc/axmcp/internal/computeruse/intervention"
	"github.com/tmc/axmcp/internal/computeruse/session"
	"github.com/tmc/axmcp/internal/skylightinput"
)

type darwinBackend struct {
	state        *darwinStateBackend
	input        computeruse.InputBackend
	screenshots  computeruse.ScreenshotBackend
	intervention computeruse.InterventionBackend
}

func newDarwinBackend(builder *appstate.Builder, monitor *intervention.Monitor) computeruse.Backend {
	if builder == nil {
		builder = appstate.NewBuilder()
	}
	report := computeruse.PlatformStatus()
	unsupported := computeruse.NewUnsupportedBackend(report)
	return &darwinBackend{
		state:        &darwinStateBackend{builder: builder},
		input:        darwinInputBackend{},
		screenshots:  unsupported.Screenshots(),
		intervention: darwinInterventionBackend{monitor: monitor},
	}
}

func (b *darwinBackend) Platform() computeruse.PlatformReport {
	return computeruse.PlatformStatus()
}

func (b *darwinBackend) State() computeruse.StateBackend {
	return b.state
}

func (b *darwinBackend) Input() computeruse.InputBackend {
	return b.input
}

func (b *darwinBackend) Screenshots() computeruse.ScreenshotBackend {
	return b.screenshots
}

func (b *darwinBackend) Intervention() computeruse.InterventionBackend {
	return b.intervention
}

type darwinStateBackend struct {
	builder *appstate.Builder
}

func (b *darwinStateBackend) ListApps(ctx context.Context) ([]computeruse.AppInfo, error) {
	return appstate.ListApps(ctx)
}

func (b *darwinStateBackend) ResolveApp(ctx context.Context, selector string) (computeruse.AppInfo, error) {
	return appstate.ResolveApp(ctx, selector)
}

func (b *darwinStateBackend) BuildState(ctx context.Context, req computeruse.StateRequest) (computeruse.Snapshot, error) {
	builder := b.builder
	if builder == nil {
		builder = appstate.NewBuilder()
	}
	return builder.Build(ctx, req.App, req.WindowTitle, req.Instructions)
}

type darwinInputBackend struct{}

func (darwinInputBackend) ClickElement(_ context.Context, snapshot computeruse.Snapshot, index int, opts computeruse.ClickOptions) error {
	actionSnapshot, err := requireActionSnapshot(snapshot)
	if err != nil {
		return err
	}
	el, _, err := actionSnapshot.Resolve(index)
	if err != nil {
		return err
	}
	return input.ClickElement(el, opts.Button, opts.ClickCount)
}

func (darwinInputBackend) ClickPoint(_ context.Context, snapshot computeruse.Snapshot, point computeruse.Point, opts computeruse.ClickOptions) error {
	actionSnapshot, err := requireActionSnapshot(snapshot)
	if err != nil {
		return err
	}
	state := snapshot.State()
	local, err := input.ScreenshotPointToWindowLocal(state.Window, point.X, point.Y)
	if err != nil {
		return err
	}
	clickCount := opts.ClickCount
	if clickCount <= 0 {
		clickCount = 1
	}
	if opts.ForegroundHID {
		if err := activatePID(state.App.PID); err != nil {
			return err
		}
	} else if canUseSkyLightPixelClick(opts.Button, clickCount, state) {
		screenPoint, err := coords.WindowLocalToScreen(state.Window, coords.Point{X: local.X, Y: local.Y})
		if err != nil {
			return err
		}
		screen := skylightinput.Point{X: float64(screenPoint.X), Y: float64(screenPoint.Y)}
		windowLocal := skylightinput.Point{X: float64(local.X), Y: float64(local.Y)}
		if err := skylightinput.MouseClick(int32(state.App.PID), screen, windowLocal, state.Window.WindowID, clickCount); err == nil {
			return nil
		}
	}
	root, _, err := actionSnapshot.Resolve(0)
	if err != nil {
		return err
	}
	return input.ClickElementAt(root, local, opts.Button, clickCount)
}

func (darwinInputBackend) Drag(_ context.Context, snapshot computeruse.Snapshot, start, end computeruse.Point, opts computeruse.DragOptions) error {
	actionSnapshot, err := requireActionSnapshot(snapshot)
	if err != nil {
		return err
	}
	state := snapshot.State()
	root, _, err := actionSnapshot.Resolve(0)
	if err != nil {
		return err
	}
	localStart, err := input.ScreenshotPointToWindowLocal(state.Window, start.X, start.Y)
	if err != nil {
		return err
	}
	localEnd, err := input.ScreenshotPointToWindowLocal(state.Window, end.X, end.Y)
	if err != nil {
		return err
	}
	button := opts.Button
	if button == "" {
		button = "left"
	}
	return input.DragElement(root, localStart, localEnd, button)
}

func (darwinInputBackend) ScrollElement(_ context.Context, snapshot computeruse.Snapshot, index int, opts computeruse.ScrollOptions) error {
	actionSnapshot, err := requireActionSnapshot(snapshot)
	if err != nil {
		return err
	}
	el, _, err := actionSnapshot.Resolve(index)
	if err != nil {
		return err
	}
	return input.ScrollElement(el, opts.Direction, opts.Pages)
}

func (darwinInputBackend) PerformSecondaryAction(_ context.Context, snapshot computeruse.Snapshot, index int, action string) error {
	actionSnapshot, err := requireActionSnapshot(snapshot)
	if err != nil {
		return err
	}
	el, _, err := actionSnapshot.Resolve(index)
	if err != nil {
		return err
	}
	return el.PerformAction(action)
}

func (darwinInputBackend) SetValue(_ context.Context, snapshot computeruse.Snapshot, index int, value string) error {
	actionSnapshot, err := requireActionSnapshot(snapshot)
	if err != nil {
		return err
	}
	el, _, err := actionSnapshot.Resolve(index)
	if err != nil {
		return err
	}
	return el.SetValue(value)
}

func (darwinInputBackend) PressKey(_ context.Context, snapshot computeruse.Snapshot, key string) error {
	if snapshot == nil {
		return fmt.Errorf("nil snapshot")
	}
	return input.SendKeyComboToPID(int32(snapshot.State().App.PID), key)
}

func (darwinInputBackend) TypeText(_ context.Context, snapshot computeruse.Snapshot, index *int, text string) error {
	actionSnapshot, err := requireActionSnapshot(snapshot)
	if err != nil {
		return err
	}
	if index != nil {
		el, _, err := actionSnapshot.Resolve(*index)
		if err != nil {
			return err
		}
		endTypingCursor := beginTypingCursor(el)
		defer endTypingCursor()
		return el.TypeText(text)
	}
	root, _, err := actionSnapshot.Resolve(0)
	if err != nil {
		return err
	}
	app := root.Application()
	if app == nil {
		return fmt.Errorf("no active application for %q", snapshot.State().App.Name)
	}
	el := app.FocusedElement()
	if el == nil {
		return fmt.Errorf("no focused element found")
	}
	endTypingCursor := beginTypingCursor(el)
	defer endTypingCursor()
	return el.TypeText(text)
}

func requireActionSnapshot(snapshot computeruse.Snapshot) (session.Snapshot, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("nil snapshot")
	}
	actionSnapshot, ok := snapshot.(session.Snapshot)
	if !ok {
		return nil, fmt.Errorf("snapshot does not support element resolution")
	}
	return actionSnapshot, nil
}

type darwinInterventionBackend struct {
	monitor *intervention.Monitor
}

func (b darwinInterventionBackend) Start() error {
	if b.monitor == nil {
		return nil
	}
	return b.monitor.Start()
}

func (b darwinInterventionBackend) Close() error {
	if b.monitor == nil {
		return nil
	}
	b.monitor.Close()
	return nil
}

func (b darwinInterventionBackend) Status(_ context.Context) (computeruse.InterventionStatus, error) {
	if b.monitor == nil {
		return computeruse.InterventionStatus{}, nil
	}
	status, blocked := b.monitor.Blocked(time.Now())
	return computeruse.InterventionStatus{
		Enabled:     status.Enabled,
		Blocked:     blocked,
		LastType:    status.LastType,
		LastKind:    status.LastKind,
		LastPID:     status.LastPID,
		QuietMillis: status.QuietPeriod.Milliseconds(),
	}, nil
}
