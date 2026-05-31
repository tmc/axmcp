package linuxstate

import (
	"context"
	"encoding/base64"
	"errors"
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

// NativeElement identifies a Linux UI element retained in a snapshot.
type NativeElement struct {
	WindowID   string
	BusName    string
	ObjectPath string
}

// AccessibilityNode is one node read from a Linux accessibility tree.
type AccessibilityNode struct {
	Native           NativeElement
	Role             string
	Title            string
	Value            string
	Description      string
	Identifier       string
	Rect             Rect
	Enabled          bool
	Settable         bool
	SecondaryActions []string
	Children         []AccessibilityNode
}

// Rect is an element rectangle in screen coordinates.
type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// Backend builds app state from X11 top-level windows.
type Backend struct {
	run           func(context.Context, string, ...string) ([]byte, error)
	accessibility func(context.Context, Window) (AccessibilityNode, error)
	atspiAction   accessibilityActionRunner
	atspiSetValue accessibilityValueRunner
}

var _ computeruse.StateBackend = Backend{}
var _ computeruse.InputBackend = Backend{}

type accessibilityActionRunner func(context.Context, accessibilityAction) error

type accessibilityAction struct {
	Native NativeElement
	Name   string
}

type accessibilityValueRunner func(context.Context, accessibilityValue) error

type accessibilityValue struct {
	Native NativeElement
	Value  string
}

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
	}
	tree, nodes, elements, err := b.buildTree(ctx, win)
	if err != nil {
		return nil, err
	}
	state.Tree = tree
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
	return &Snapshot{state: state, window: win, nodes: nodes, elements: elements}, nil
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

func (b Backend) buildTree(ctx context.Context, win Window) ([]computeruse.ElementNode, map[int]computeruse.ElementNode, map[int]NativeElement, error) {
	root, err := b.accessibilityTree(ctx, win)
	if err != nil {
		if b.accessibility != nil || ctx.Err() != nil || !errors.Is(err, computeruse.ErrPlatformUnsupported) {
			return nil, nil, nil, fmt.Errorf("read AT-SPI tree: %w", err)
		}
		root = fallbackAccessibilityRoot(win)
	}
	if root.isEmpty() {
		root = fallbackAccessibilityRoot(win)
	}
	tree, nodes, elements := flattenAccessibilityTree(win, root)
	return tree, nodes, elements, nil
}

func (b Backend) accessibilityTree(ctx context.Context, win Window) (AccessibilityNode, error) {
	if b.accessibility != nil {
		return b.accessibility(ctx, win)
	}
	return readAccessibilityTree(ctx, win)
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
	state    computeruse.AppState
	window   Window
	nodes    map[int]computeruse.ElementNode
	elements map[int]NativeElement
}

func (s *Snapshot) State() computeruse.AppState {
	if s == nil {
		return computeruse.AppState{}
	}
	return s.state
}

func (s *Snapshot) NativeElement(index int) (NativeElement, computeruse.ElementNode, error) {
	if s == nil {
		return NativeElement{}, computeruse.ElementNode{}, fmt.Errorf("nil snapshot")
	}
	node, ok := s.nodes[index]
	if !ok {
		return NativeElement{}, computeruse.ElementNode{}, fmt.Errorf("unknown element_index %d", index)
	}
	return s.elements[index], node, nil
}

func (s *Snapshot) Close() error {
	return nil
}

func fallbackAccessibilityRoot(win Window) AccessibilityNode {
	return AccessibilityNode{
		Native: NativeElement{
			WindowID: win.ID,
		},
		Role:    "Window",
		Title:   win.Title,
		Rect:    Rect{X: win.X, Y: win.Y, Width: win.Width, Height: win.Height},
		Enabled: true,
	}
}

func (n AccessibilityNode) isEmpty() bool {
	return n.Native == (NativeElement{}) &&
		strings.TrimSpace(n.Role) == "" &&
		strings.TrimSpace(n.Title) == "" &&
		n.Rect == (Rect{}) &&
		len(n.Children) == 0
}

func flattenAccessibilityTree(win Window, root AccessibilityNode) ([]computeruse.ElementNode, map[int]computeruse.ElementNode, map[int]NativeElement) {
	type queueItem struct {
		parent int
		node   AccessibilityNode
	}
	queue := []queueItem{{parent: -1, node: root}}
	tree := make([]computeruse.ElementNode, 0, 128)
	nodes := make(map[int]computeruse.ElementNode)
	elements := make(map[int]NativeElement)

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		index := len(tree)
		node := elementNode(win, item.node, item.parent, index)
		tree = append(tree, node)
		nodes[index] = node
		elements[index] = nativeElement(win, item.node)
		for _, child := range item.node.Children {
			queue = append(queue, queueItem{parent: index, node: child})
		}
	}
	return tree, nodes, elements
}

func elementNode(win Window, src AccessibilityNode, parent, index int) computeruse.ElementNode {
	rect := src.Rect
	if rect.Width <= 0 || rect.Height <= 0 {
		rect = Rect{X: win.X, Y: win.Y, Width: win.Width, Height: win.Height}
	}
	role := strings.TrimSpace(src.Role)
	if role == "" && index == 0 {
		role = "Window"
	}
	title := strings.TrimSpace(src.Title)
	if title == "" && index == 0 {
		title = win.Title
	}
	return computeruse.ElementNode{
		Index:            index,
		ParentIndex:      parent,
		Role:             role,
		Title:            title,
		Value:            strings.TrimSpace(src.Value),
		Description:      strings.TrimSpace(src.Description),
		Identifier:       strings.TrimSpace(src.Identifier),
		X:                rect.X - win.X,
		Y:                rect.Y - win.Y,
		Width:            rect.Width,
		Height:           rect.Height,
		Enabled:          src.Enabled,
		Settable:         src.Settable,
		SecondaryActions: append([]string(nil), src.SecondaryActions...),
	}
}

func nativeElement(win Window, node AccessibilityNode) NativeElement {
	native := node.Native
	if native.WindowID == "" {
		native.WindowID = win.ID
	}
	return native
}

func (b Backend) ClickElement(ctx context.Context, snapshot computeruse.Snapshot, index int, opts computeruse.ClickOptions) error {
	s, err := linuxSnapshot(snapshot)
	if err != nil {
		return err
	}
	if index != 0 {
		native, node, err := s.NativeElement(index)
		if err != nil {
			return err
		}
		action, ok, err := clickElementAction(node, opts)
		if err != nil {
			return err
		}
		if ok {
			err := b.runAccessibilityAction(ctx, accessibilityAction{Native: native, Name: action})
			if err == nil {
				return nil
			}
			if !errors.Is(err, computeruse.ErrPlatformUnsupported) {
				return err
			}
		}
		return b.clickElementByGeometry(ctx, s, node, opts)
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
	return b.clickWindowLocal(ctx, s, local, opts)
}

func (b Backend) clickElementByGeometry(ctx context.Context, s *Snapshot, node computeruse.ElementNode, opts computeruse.ClickOptions) error {
	if node.Width <= 0 || node.Height <= 0 {
		return fmt.Errorf("element has empty bounds")
	}
	local := coords.Point{X: node.X + node.Width/2, Y: node.Y + node.Height/2}
	return b.clickWindowLocal(ctx, s, local, opts)
}

func (b Backend) clickWindowLocal(ctx context.Context, s *Snapshot, local coords.Point, opts computeruse.ClickOptions) error {
	button, err := xdotoolButton(opts.Button)
	if err != nil {
		return err
	}
	if opts.ForegroundHID {
		screen, err := coords.WindowLocalToScreen(s.state.Window, local)
		if err != nil {
			return err
		}
		if err := b.runXDoTool(ctx, s.window, "windowactivate", "--sync", s.window.ID); err != nil {
			return err
		}
		if err := b.runXDoTool(ctx, s.window, "mousemove", strconv.Itoa(screen.X), strconv.Itoa(screen.Y)); err != nil {
			return err
		}
		for range normalizeClickCount(opts.ClickCount) {
			if err := b.runXDoTool(ctx, s.window, "click", button); err != nil {
				return err
			}
		}
		return nil
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
	_, node, err := s.NativeElement(index)
	if err != nil {
		return err
	}
	if node.Width <= 0 || node.Height <= 0 {
		return fmt.Errorf("element has empty bounds")
	}
	button, err := scrollButton(opts.Direction)
	if err != nil {
		return err
	}
	local := coords.Point{X: node.X + node.Width/2, Y: node.Y + node.Height/2}
	if err := b.runXDoTool(ctx, s.window, "mousemove", "--window", s.window.ID, strconv.Itoa(local.X), strconv.Itoa(local.Y)); err != nil {
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

func (b Backend) PerformSecondaryAction(ctx context.Context, snapshot computeruse.Snapshot, index int, action string) error {
	s, err := linuxSnapshot(snapshot)
	if err != nil {
		return err
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return fmt.Errorf("missing secondary action")
	}
	native, _, err := s.NativeElement(index)
	if err != nil {
		return err
	}
	return b.runAccessibilityAction(ctx, accessibilityAction{Native: native, Name: action})
}

func (b Backend) SetValue(ctx context.Context, snapshot computeruse.Snapshot, index int, value string) error {
	s, err := linuxSnapshot(snapshot)
	if err != nil {
		return err
	}
	native, _, err := s.NativeElement(index)
	if err != nil {
		return err
	}
	return b.runAccessibilityValue(ctx, accessibilityValue{Native: native, Value: value})
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
		_, node, err := s.NativeElement(*elementIndex)
		if err != nil {
			return err
		}
		if node.Width <= 0 || node.Height <= 0 {
			return fmt.Errorf("element has empty bounds")
		}
		local := coords.Point{X: node.X + node.Width/2, Y: node.Y + node.Height/2}
		if err := b.runXDoTool(ctx, s.window, "mousemove", "--window", s.window.ID, strconv.Itoa(local.X), strconv.Itoa(local.Y)); err != nil {
			return err
		}
		if err := b.runXDoTool(ctx, s.window, "click", "--window", s.window.ID, "1"); err != nil {
			return err
		}
	}
	return b.runXDoTool(ctx, s.window, "type", "--window", s.window.ID, "--", text)
}

func (b Backend) runAccessibilityAction(ctx context.Context, action accessibilityAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	action.Name = strings.TrimSpace(action.Name)
	if action.Name == "" {
		return fmt.Errorf("missing AT-SPI action")
	}
	if strings.TrimSpace(action.Native.BusName) == "" || strings.TrimSpace(action.Native.ObjectPath) == "" {
		return computeruse.PlatformUnsupported("perform AT-SPI action")
	}
	run := b.atspiAction
	if run == nil {
		run = performATSPIAction
	}
	return run(ctx, action)
}

func (b Backend) runAccessibilityValue(ctx context.Context, value accessibilityValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(value.Native.BusName) == "" || strings.TrimSpace(value.Native.ObjectPath) == "" {
		return computeruse.PlatformUnsupported("set value with AT-SPI")
	}
	run := b.atspiSetValue
	if run == nil {
		run = setATSPIValue
	}
	return run(ctx, value)
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

func clickElementAction(node computeruse.ElementNode, opts computeruse.ClickOptions) (string, bool, error) {
	if _, err := xdotoolButton(opts.Button); err != nil {
		return "", false, err
	}
	if opts.ForegroundHID {
		return "", false, nil
	}
	if normalizeClickCount(opts.ClickCount) != 1 {
		return "", false, nil
	}
	if button := strings.TrimSpace(opts.Button); button != "" && !strings.EqualFold(button, "left") {
		return "", false, nil
	}
	for _, action := range node.SecondaryActions {
		if atspiActionIsClick(action) {
			return strings.TrimSpace(action), true, nil
		}
	}
	return "", false, nil
}

func atspiActionIsClick(action string) bool {
	switch atspiActionNameKey(action) {
	case "activate", "click", "invoke", "press":
		return true
	default:
		return false
	}
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
