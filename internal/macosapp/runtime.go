package macosapp

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/apple/x/axuiautomation"
)

type RunningApp struct {
	Name     string `json:"name,omitempty"`
	BundleID string `json:"bundle_id,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

type AppSelector struct {
	BundleID string `json:"bundle_id,omitempty"`
	Name     string `json:"name,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

type WindowInfo struct {
	Title      string `json:"title,omitempty"`
	X          int    `json:"x,omitempty"`
	Y          int    `json:"y,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	ChildCount int    `json:"child_count,omitempty"`
}

type ReadyState struct {
	Ready      bool       `json:"ready"`
	Name       string     `json:"name,omitempty"`
	BundleID   string     `json:"bundle_id,omitempty"`
	PID        int        `json:"pid,omitempty"`
	Frontmost  bool       `json:"frontmost,omitempty"`
	MainWindow WindowInfo `json:"main_window,omitempty"`
}

type WaitOptions struct {
	Timeout            time.Duration
	PollInterval       time.Duration
	RequireWindow      bool
	RequireWindowTitle bool
	RequireContent     bool
}

func ListRunningApps(ctx context.Context) ([]RunningApp, error) {
	cmd := exec.CommandContext(ctx, "lsappinfo", "list")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lsappinfo list: %w", err)
	}
	return ParseLSAppInfoList(string(out)), nil
}

func Launch(ctx context.Context, bundlePath, bundleID string, frontmost bool) (*RunningApp, error) {
	cmd := exec.CommandContext(ctx, "open", bundlePath)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("launch %s: %w", bundlePath, err)
	}
	if bundleID == "" {
		return nil, nil
	}
	if frontmost {
		if err := Activate(ctx, bundleID); err != nil {
			return nil, err
		}
	}
	return WaitForProcess(ctx, AppSelector{BundleID: bundleID})
}

func Activate(ctx context.Context, bundleID string) error {
	if bundleID == "" {
		return nil
	}
	script := fmt.Sprintf(`tell application id "%s" to activate`, bundleID)
	if out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("activate %s: %w: %s", bundleID, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func WaitForProcess(ctx context.Context, selector AppSelector) (*RunningApp, error) {
	for {
		apps, err := ListRunningApps(ctx)
		if err != nil {
			return nil, err
		}
		if app := FindRunningApp(apps, selector); app != nil {
			return app, nil
		}
		if err := sleepOrDone(ctx, 200*time.Millisecond); err != nil {
			return nil, fmt.Errorf("wait for app process: %w", err)
		}
	}
}

func WaitUntilReady(ctx context.Context, selector AppSelector, opts WaitOptions) (*ReadyState, error) {
	opts = normalizeWaitOptions(opts)
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	for {
		apps, err := ListRunningApps(ctx)
		if err != nil {
			return nil, err
		}
		app := FindRunningApp(apps, selector)
		if app != nil {
			state := &ReadyState{
				Name:     app.Name,
				BundleID: app.BundleID,
				PID:      app.PID,
			}
			if !opts.RequireWindow {
				state.Ready = true
				return state, nil
			}

			window, ok := findReadyWindow(*app, opts)
			if ok {
				state.Ready = true
				state.MainWindow = window
				return state, nil
			}
		}
		if err := sleepOrDone(ctx, opts.PollInterval); err != nil {
			return nil, fmt.Errorf("wait for app readiness: %w", err)
		}
	}
}

func ParseLSAppInfoList(output string) []RunningApp {
	var apps []RunningApp
	var cur RunningApp
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.Contains(line, ") \"") && strings.Contains(line, "ASN:"):
			if cur.BundleID != "" {
				apps = append(apps, cur)
			}
			cur = RunningApp{}
			quoted := line[strings.Index(line, "\"")+1:]
			cur.Name = quoted[:strings.Index(quoted, "\"")]
		case strings.HasPrefix(line, "bundleID="):
			value := strings.Trim(strings.TrimPrefix(line, "bundleID="), `"`)
			if value != "" && value != "[ NULL ]" {
				cur.BundleID = value
			}
		case strings.HasPrefix(line, "pid = "):
			value := strings.TrimPrefix(line, "pid = ")
			if i := strings.IndexAny(value, " \t"); i > 0 {
				value = value[:i]
			}
			cur.PID, _ = strconv.Atoi(value)
		}
	}
	if cur.BundleID != "" {
		apps = append(apps, cur)
	}
	return apps
}

func FindRunningApp(apps []RunningApp, selector AppSelector) *RunningApp {
	for i := range apps {
		app := apps[i]
		switch {
		case selector.PID > 0 && app.PID == selector.PID:
			return &apps[i]
		case selector.BundleID != "" && app.BundleID == selector.BundleID:
			return &apps[i]
		case selector.Name != "" && strings.EqualFold(app.Name, selector.Name):
			return &apps[i]
		}
	}
	return nil
}

func ChooseReadyWindow(windows []WindowInfo, opts WaitOptions) (WindowInfo, bool) {
	for _, window := range windows {
		if window.Width <= 0 || window.Height <= 0 {
			continue
		}
		if opts.RequireWindowTitle && strings.TrimSpace(window.Title) == "" {
			continue
		}
		if opts.RequireContent && window.ChildCount == 0 {
			continue
		}
		return window, true
	}
	return WindowInfo{}, false
}

func normalizeWaitOptions(opts WaitOptions) WaitOptions {
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 250 * time.Millisecond
	}
	return opts
}

func findReadyWindow(app RunningApp, opts WaitOptions) (WindowInfo, bool) {
	axuiautomation.SpinRunLoop(100 * time.Millisecond)
	axApp := axuiautomation.NewApplicationFromPID(int32(app.PID))
	if axApp == nil {
		return WindowInfo{}, false
	}
	defer axApp.Close()

	var windows []WindowInfo
	for _, win := range axApp.WindowList() {
		if win == nil {
			continue
		}
		x, y := win.Position()
		width, height := win.Size()
		windows = append(windows, WindowInfo{
			Title:      strings.TrimSpace(win.Title()),
			X:          x,
			Y:          y,
			Width:      width,
			Height:     height,
			ChildCount: len(win.Children()),
		})
	}
	return ChooseReadyWindow(windows, opts)
}

func sleepOrDone(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
