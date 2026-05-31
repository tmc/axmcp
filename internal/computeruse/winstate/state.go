package winstate

import (
	"context"
	"encoding/base64"
	"errors"
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

// NativeElement identifies a Windows UI element retained in a snapshot.
type NativeElement struct {
	WindowHandle     uintptr
	AutomationHandle uintptr
}

// AutomationNode is one node read from a Windows automation tree.
type AutomationNode struct {
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
	Children         []AutomationNode
	release          func()
}

// Backend builds app state from top-level Win32 windows.
type Backend struct {
	windows    func(context.Context) ([]Window, error)
	screenshot func(context.Context, Window) ([]byte, error)
	automation func(context.Context, Window) (AutomationNode, error)
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
	}
	tree, nodes, elements, releases, err := b.buildTree(ctx, win)
	if err != nil {
		return nil, err
	}
	state.Tree = tree
	if req.Instructions != nil {
		state.Instructions = req.Instructions.Instructions(state.App)
	}
	pngData, err := b.captureScreenshot(ctx, win)
	if err != nil {
		releaseAll(releases)
		return nil, err
	}
	pngData, cfg, err := computeruse.NormalizeScreenshotPNG(pngData, computeruse.MaxScreenshotLongSide)
	if err != nil {
		releaseAll(releases)
		return nil, err
	}
	state.ScreenshotPNGBase64 = base64.StdEncoding.EncodeToString(pngData)
	state.Window.ScreenshotWidth = cfg.Width
	state.Window.ScreenshotHeight = cfg.Height
	return &Snapshot{state: state, window: win, nodes: nodes, elements: elements, releases: releases}, nil
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

func (b Backend) buildTree(ctx context.Context, win Window) ([]computeruse.ElementNode, map[int]computeruse.ElementNode, map[int]NativeElement, []func(), error) {
	root, err := b.automationTree(ctx, win)
	if err != nil {
		if b.automation != nil || ctx.Err() != nil ||
			(!errors.Is(err, computeruse.ErrPlatformUnsupported) && !errors.Is(err, errAutomationUnavailable)) {
			return nil, nil, nil, nil, fmt.Errorf("read UI Automation tree: %w", err)
		}
		root = fallbackAutomationRoot(win)
	}
	if root.isEmpty() {
		root = fallbackAutomationRoot(win)
	}
	tree, nodes, elements, releases := flattenAutomationTree(win, root)
	return tree, nodes, elements, releases, nil
}

func (b Backend) automationTree(ctx context.Context, win Window) (AutomationNode, error) {
	if b.automation != nil {
		return b.automation(ctx, win)
	}
	return readAutomationTree(ctx, win)
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
	state    computeruse.AppState
	window   Window
	nodes    map[int]computeruse.ElementNode
	elements map[int]NativeElement
	releases []func()
	closed   bool
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
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	releaseAll(s.releases)
	s.releases = nil
	return nil
}

var errAutomationUnavailable = errors.New("UI Automation unavailable")

func releaseAll(releases []func()) {
	for _, release := range releases {
		if release != nil {
			release()
		}
	}
}

func fallbackAutomationRoot(win Window) AutomationNode {
	return AutomationNode{
		Native: NativeElement{
			WindowHandle: win.Handle,
		},
		Role:    "Window",
		Title:   win.Title,
		Rect:    win.Rect,
		Enabled: true,
	}
}

func (n AutomationNode) isEmpty() bool {
	return n.Native == (NativeElement{}) &&
		strings.TrimSpace(n.Role) == "" &&
		strings.TrimSpace(n.Title) == "" &&
		n.Rect == (Rect{}) &&
		len(n.Children) == 0
}

func flattenAutomationTree(win Window, root AutomationNode) ([]computeruse.ElementNode, map[int]computeruse.ElementNode, map[int]NativeElement, []func()) {
	type queueItem struct {
		parent int
		node   AutomationNode
	}
	queue := []queueItem{{parent: -1, node: root}}
	tree := make([]computeruse.ElementNode, 0, 128)
	nodes := make(map[int]computeruse.ElementNode)
	elements := make(map[int]NativeElement)
	var releases []func()

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		index := len(tree)
		node := elementNode(win, item.node, item.parent, index)
		tree = append(tree, node)
		nodes[index] = node
		elements[index] = nativeElement(win, item.node)
		if item.node.release != nil {
			releases = append(releases, item.node.release)
		}
		for _, child := range item.node.Children {
			queue = append(queue, queueItem{parent: index, node: child})
		}
	}
	return tree, nodes, elements, releases
}

func elementNode(win Window, src AutomationNode, parent, index int) computeruse.ElementNode {
	rect := src.Rect
	if rect.Width <= 0 || rect.Height <= 0 {
		rect = win.Rect
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
		X:                rect.X - win.Rect.X,
		Y:                rect.Y - win.Rect.Y,
		Width:            rect.Width,
		Height:           rect.Height,
		Enabled:          src.Enabled,
		Settable:         src.Settable,
		SecondaryActions: append([]string(nil), src.SecondaryActions...),
	}
}

func nativeElement(win Window, node AutomationNode) NativeElement {
	native := node.Native
	if native.WindowHandle == 0 {
		native.WindowHandle = win.Handle
	}
	return native
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
