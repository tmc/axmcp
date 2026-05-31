package linuxstate

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/coords"
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
var _ computeruse.InputBackend = Backend{}

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
			X:       0,
			Y:       0,
			Width:   win.Width,
			Height:  win.Height,
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

func (b Backend) captureScreenshot(ctx context.Context, win Window) ([]byte, error) {
	run := b.run
	if run == nil {
		run = runCommand
	}
	return captureWindowPNG(ctx, run, win)
}

func captureWindowPNG(ctx context.Context, run func(context.Context, string, ...string) ([]byte, error), win Window) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(win.ID) == "" {
		return nil, fmt.Errorf("missing X11 window id")
	}
	if win.Width <= 0 || win.Height <= 0 {
		return nil, fmt.Errorf("window has empty bounds")
	}
	out, err := run(ctx, "import", "-window", win.ID, "png:-")
	if err != nil {
		return nil, fmt.Errorf("capture X11 window screenshot with import: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("capture X11 window screenshot with import: empty output")
	}
	return out, nil
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

func (b Backend) ClickElement(ctx context.Context, snapshot computeruse.Snapshot, index int, opts computeruse.ClickOptions) error {
	s, err := linuxSnapshot(snapshot)
	if err != nil {
		return err
	}
	if index != 0 {
		return computeruse.PlatformUnsupported("click element with AT-SPI")
	}
	point := computeruse.Point{
		X: s.state.Window.ScreenshotWidth / 2,
		Y: s.state.Window.ScreenshotHeight / 2,
	}
	return b.ClickPoint(ctx, s, point, opts)
}

func (b Backend) ClickPoint(ctx context.Context, snapshot computeruse.Snapshot, point computeruse.Point, opts computeruse.ClickOptions) error {
	s, err := linuxSnapshot(snapshot)
	if err != nil {
		return err
	}
	local, err := coords.ScreenshotPointToWindowLocal(s.state.Window, point.X, point.Y)
	if err != nil {
		return err
	}
	button, err := xdotoolButton(opts.Button)
	if err != nil {
		return err
	}
	if err := b.runXDoTool(ctx, s.window, "mousemove", "--window", s.window.ID, strconv.Itoa(local.X), strconv.Itoa(local.Y)); err != nil {
		return err
	}
	for range normalizeClickCount(opts.ClickCount) {
		if err := b.runXDoTool(ctx, s.window, "click", "--window", s.window.ID, button); err != nil {
			return err
		}
	}
	return nil
}

func (b Backend) Drag(ctx context.Context, snapshot computeruse.Snapshot, start, end computeruse.Point, opts computeruse.DragOptions) error {
	s, err := linuxSnapshot(snapshot)
	if err != nil {
		return err
	}
	startLocal, err := coords.ScreenshotPointToWindowLocal(s.state.Window, start.X, start.Y)
	if err != nil {
		return err
	}
	endLocal, err := coords.ScreenshotPointToWindowLocal(s.state.Window, end.X, end.Y)
	if err != nil {
		return err
	}
	button, err := xdotoolButton(opts.Button)
	if err != nil {
		return err
	}
	steps := [][]string{
		{"mousemove", "--window", s.window.ID, strconv.Itoa(startLocal.X), strconv.Itoa(startLocal.Y)},
		{"mousedown", "--window", s.window.ID, button},
		{"mousemove", "--window", s.window.ID, strconv.Itoa(endLocal.X), strconv.Itoa(endLocal.Y)},
		{"mouseup", "--window", s.window.ID, button},
	}
	for _, step := range steps {
		if err := b.runXDoTool(ctx, s.window, step...); err != nil {
			return err
		}
	}
	return nil
}

func (b Backend) ScrollElement(ctx context.Context, snapshot computeruse.Snapshot, index int, opts computeruse.ScrollOptions) error {
	s, err := linuxSnapshot(snapshot)
	if err != nil {
		return err
	}
	if index != 0 {
		return computeruse.PlatformUnsupported("scroll element with AT-SPI")
	}
	button, err := scrollButton(opts.Direction)
	if err != nil {
		return err
	}
	count := scrollCount(opts.Pages)
	for range count {
		if err := b.runXDoTool(ctx, s.window, "click", "--window", s.window.ID, button); err != nil {
			return err
		}
	}
	return nil
}

func (b Backend) PerformSecondaryAction(context.Context, computeruse.Snapshot, int, string) error {
	return computeruse.PlatformUnsupported("perform secondary action with AT-SPI")
}

func (b Backend) SetValue(context.Context, computeruse.Snapshot, int, string) error {
	return computeruse.PlatformUnsupported("set value with AT-SPI")
}

func (b Backend) PressKey(ctx context.Context, snapshot computeruse.Snapshot, key string) error {
	s, err := linuxSnapshot(snapshot)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("missing key")
	}
	return b.runXDoTool(ctx, s.window, "key", "--window", s.window.ID, key)
}

func (b Backend) TypeText(ctx context.Context, snapshot computeruse.Snapshot, elementIndex *int, text string) error {
	s, err := linuxSnapshot(snapshot)
	if err != nil {
		return err
	}
	if elementIndex != nil && *elementIndex != 0 {
		return computeruse.PlatformUnsupported("type into element with AT-SPI")
	}
	return b.runXDoTool(ctx, s.window, "type", "--window", s.window.ID, "--", text)
}

func linuxSnapshot(snapshot computeruse.Snapshot) (*Snapshot, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("missing state snapshot")
	}
	s, ok := snapshot.(*Snapshot)
	if !ok {
		return nil, fmt.Errorf("state snapshot is not from linux backend")
	}
	return s, nil
}

func (b Backend) runXDoTool(ctx context.Context, win Window, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("missing xdotool command")
	}
	if strings.TrimSpace(win.ID) == "" {
		return fmt.Errorf("missing X11 window id")
	}
	run := b.run
	if run == nil {
		run = runCommand
	}
	if _, err := run(ctx, "xdotool", args...); err != nil {
		return fmt.Errorf("run xdotool %s: %w", args[0], err)
	}
	return nil
}

func normalizeClickCount(clickCount int) int {
	if clickCount < 1 {
		return 1
	}
	return clickCount
}

func xdotoolButton(button string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(button)) {
	case "", "left":
		return "1", nil
	case "middle":
		return "2", nil
	case "right":
		return "3", nil
	default:
		return "", fmt.Errorf("invalid button %q; use left, right, or middle", button)
	}
}

func scrollButton(direction string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "", "down":
		return "5", nil
	case "up":
		return "4", nil
	case "left":
		return "6", nil
	case "right":
		return "7", nil
	default:
		return "", fmt.Errorf("invalid scroll direction %q; use up, down, left, or right", direction)
	}
}

func scrollCount(pages float64) int {
	if pages == 0 {
		pages = 1
	}
	if pages < 0 {
		pages = -pages
	}
	count := int(math.Round(pages * 5))
	if count < 1 {
		return 1
	}
	return count
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
