package appstate

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/macosapp"
	"github.com/tmc/axmcp/internal/purego/cfhandle"
)

const axTimeout = 5

var axSetMessagingTimeout func(element uintptr, timeoutInSeconds float32) int32
var axCopyActionNames func(element uintptr, names *uintptr) int32

func init() {
	lib, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	purego.RegisterLibFunc(&axSetMessagingTimeout, lib, "AXUIElementSetMessagingTimeout")
	purego.RegisterLibFunc(&axCopyActionNames, lib, "AXUIElementCopyActionNames")
}

type Builder struct{}

func NewBuilder() *Builder {
	return &Builder{}
}

type Snapshot struct {
	state    computeruse.AppState
	app      *axuiautomation.Application
	elements map[int]*axuiautomation.Element
	nodes    map[int]computeruse.ElementNode
	owned    []*axuiautomation.Element
}

func ListApps(ctx context.Context) ([]computeruse.AppInfo, error) {
	apps, err := macosapp.ListRunningApps(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]computeruse.AppInfo, 0, len(apps))
	for _, app := range apps {
		out = append(out, computeruse.AppInfo{
			Name:     app.Name,
			BundleID: app.BundleID,
			PID:      app.PID,
		})
	}
	return out, nil
}

func ResolveApp(ctx context.Context, selector string) (computeruse.AppInfo, error) {
	app, info, err := openApp(ctx, selector)
	if err != nil {
		return computeruse.AppInfo{}, err
	}
	app.Close()
	return info, nil
}

func (b *Builder) Build(ctx context.Context, selector, windowTitle string, instructions computeruse.InstructionProvider) (*Snapshot, error) {
	app, info, err := openApp(ctx, selector)
	if err != nil {
		return nil, err
	}
	window, err := selectWindow(app, windowTitle)
	if err != nil {
		app.Close()
		return nil, err
	}
	return finishSnapshot(ctx, app, info, window, instructions)
}

// BuildWindow captures one running process's exact window. A zero windowID
// selects its focused window; a positive ID must match exactly. It never launches
// an app or falls back to another window. The caller owns the returned snapshot.
// Process-instance validation across capture is the action runner's responsibility.
func (b *Builder) BuildWindow(ctx context.Context, pid int32, windowID uint32, instructions computeruse.InstructionProvider) (*Snapshot, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("pid must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return b.buildWindow(ctx, pid, windowID, nil, instructions)
}

// BuildWindowElement captures the live window matching a retained accessibility
// element in the exact process's window list. It never falls back to a window
// title, numeric ID, or focused window. The caller retains ownership of window;
// the returned snapshot independently owns its enumerated handles. The caller
// holds window alive and validates the process instance across capture.
func (b *Builder) BuildWindowElement(ctx context.Context, pid int32, window *axuiautomation.Element, instructions computeruse.InstructionProvider) (*Snapshot, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("pid must be positive")
	}
	if window == nil || window.Ref() == 0 {
		return nil, fmt.Errorf("window is required")
	}
	if err := boundAXTimeout(ctx, window); err != nil {
		return nil, err
	}
	if !window.Exists() {
		return nil, fmt.Errorf("window is unavailable")
	}
	return b.buildWindow(ctx, pid, 0, window, instructions)
}

func (b *Builder) buildWindow(ctx context.Context, pid int32, windowID uint32, expected *axuiautomation.Element, instructions computeruse.InstructionProvider) (*Snapshot, error) {
	app := axuiautomation.NewApplicationFromPID(pid)
	if app == nil {
		return nil, fmt.Errorf("cannot connect to pid %d", pid)
	}
	fail := func(err error) (*Snapshot, error) { app.Close(); return nil, err }
	if err := boundAXTimeout(ctx, app.Root()); err != nil {
		return fail(err)
	}
	if windowID == 0 && expected == nil {
		focused := app.FocusedElement()
		for depth := 0; focused != nil && depth < 64; depth++ {
			if err := boundAXTimeout(ctx, focused); err != nil {
				focused.Release()
				return fail(err)
			}
			windowID = focused.WindowID()
			if windowID != 0 {
				focused.Release()
				focused = nil
				break
			}
			parent := focused.Parent()
			focused.Release()
			focused = parent
		}
		if focused != nil {
			focused.Release()
		}
		if windowID == 0 {
			return fail(fmt.Errorf("focused window unavailable"))
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	windows := app.WindowList()
	ids := make([]uint32, len(windows))
	var scanErr error
	for i, window := range windows {
		if window == nil {
			continue
		}
		if err := boundAXTimeout(ctx, window); err != nil {
			scanErr = err
			break
		}
		ids[i] = window.WindowID()
	}
	index, err := exactWindowIndex(ids, windowID)
	if expected != nil {
		index, err = matchingWindowIndex(windows, expected)
	}
	if scanErr != nil {
		err = scanErr
	}
	for i, window := range windows {
		if window != nil && (err != nil || i != index) {
			window.Release()
		}
	}
	if err != nil {
		return fail(err)
	}
	info := lookupAppInfo(ctx, pid, app.BundleID(), strconv.Itoa(int(pid)))
	snapshot, err := finishSnapshot(ctx, app, info, windows[index], instructions)
	if err != nil || expected == nil {
		return snapshot, err
	}
	// Capture can pump the target application. Check membership again after it
	// returns so a closed or replaced window cannot produce a published state.
	if err := boundAXTimeout(ctx, expected); err != nil {
		snapshot.Close()
		return nil, err
	}
	if err := boundAXTimeout(ctx, app.Root()); err != nil {
		snapshot.Close()
		return nil, err
	}
	current := app.WindowList()
	_, matchErr := matchingWindowIndex(current, expected)
	for _, window := range current {
		if window != nil {
			window.Release()
		}
	}
	if matchErr != nil || !expected.Exists() {
		snapshot.Close()
		return nil, fmt.Errorf("window changed during observation")
	}
	return snapshot, nil
}

func matchingWindowIndex(windows []*axuiautomation.Element, expected *axuiautomation.Element) (int, error) {
	if expected == nil || expected.Ref() == 0 {
		return -1, fmt.Errorf("window is required")
	}
	lib, err := cfhandle.Open()
	if err != nil {
		return -1, err
	}
	found := -1
	for i, window := range windows {
		if window == nil || window.Ref() == 0 || !lib.Equal(window.Ref(), expected.Ref()) {
			continue
		}
		if found >= 0 {
			return -1, fmt.Errorf("window reference is ambiguous")
		}
		found = i
	}
	if found < 0 {
		return -1, fmt.Errorf("window reference is unavailable")
	}
	return found, nil
}

func exactWindowIndex(ids []uint32, want uint32) (int, error) {
	if want == 0 {
		return -1, fmt.Errorf("window identity unavailable")
	}
	found := -1
	for i, id := range ids {
		if id != want {
			continue
		}
		if found >= 0 {
			return -1, fmt.Errorf("window identity %d is ambiguous", want)
		}
		found = i
	}
	if found < 0 {
		return -1, fmt.Errorf("window %d unavailable", want)
	}
	return found, nil
}

// finishSnapshot owns app and window on every path.
func finishSnapshot(ctx context.Context, app *axuiautomation.Application, info computeruse.AppInfo, window *axuiautomation.Element, instructions computeruse.InstructionProvider) (*Snapshot, error) {
	if _, err := cfhandle.Open(); err != nil {
		window.Release()
		app.Close()
		return nil, err
	}
	state, elements, nodes, err := buildState(ctx, info, window, instructions)
	if err != nil {
		window.Release()
		app.Close()
		return nil, err
	}
	owned := make([]*axuiautomation.Element, 0, len(elements))
	for _, el := range elements {
		owned = append(owned, el)
	}
	return &Snapshot{state: state, app: app, elements: elements, nodes: nodes, owned: owned}, nil
}

// boundAXTimeout bounds each subsequent AX exchange. It cannot undo a call
// already in flight. Descendant handles get their own remaining-budget timeout.
func boundAXTimeout(ctx context.Context, el *axuiautomation.Element) error {
	return boundAXCalls(ctx, el, 1)
}

// boundAXCalls divides the remaining budget across calls that an AX wrapper
// performs without returning control to its caller between requests.
func boundAXCalls(ctx context.Context, el *axuiautomation.Element, calls int) error {
	if calls < 1 {
		return fmt.Errorf("invalid accessibility call count")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if el == nil {
		return fmt.Errorf("accessibility element unavailable")
	}
	timeout := float32(axTimeout)
	if deadline, ok := ctx.Deadline(); ok {
		left := time.Until(deadline).Seconds() / float64(calls)
		if left <= 0 {
			return context.DeadlineExceeded
		}
		if left < float64(timeout) {
			timeout = float32(left)
		}
	}
	if axSetMessagingTimeout == nil {
		return fmt.Errorf("accessibility timeout API unavailable")
	}
	if code := axSetMessagingTimeout(el.Ref(), timeout); code != 0 {
		return fmt.Errorf("set accessibility timeout: %d", code)
	}
	return nil
}

func (s *Snapshot) State() computeruse.AppState {
	return s.state
}

func (s *Snapshot) Resolve(index int) (*axuiautomation.Element, computeruse.ElementNode, error) {
	el, ok := s.elements[index]
	if !ok || el == nil {
		return nil, computeruse.ElementNode{}, fmt.Errorf("unknown element_index %d", index)
	}
	node, ok := s.nodes[index]
	if !ok {
		return nil, computeruse.ElementNode{}, fmt.Errorf("missing node %d", index)
	}
	return el, node, nil
}

func (s *Snapshot) App() *axuiautomation.Application {
	return s.app
}

func (s *Snapshot) Close() error {
	for i := len(s.owned) - 1; i >= 0; i-- {
		if s.owned[i] != nil {
			s.owned[i].Release()
		}
	}
	if s.app != nil {
		s.app.Close()
	}
	s.owned = nil
	s.elements = nil
	s.nodes = nil
	s.app = nil
	return nil
}

func buildState(ctx context.Context, app computeruse.AppInfo, window *axuiautomation.Element, instructions computeruse.InstructionProvider) (computeruse.AppState, map[int]*axuiautomation.Element, map[int]computeruse.ElementNode, error) {
	if err := boundAXTimeout(ctx, window); err != nil {
		return computeruse.AppState{}, nil, nil, err
	}
	png, metadata, err := captureWindow(ctx, window)
	if err != nil {
		return computeruse.AppState{}, nil, nil, err
	}
	frame := axuiautomation.Rect{
		Origin: axuiautomation.Point{X: metadata.GlobalRect.X, Y: metadata.GlobalRect.Y},
		Size:   axuiautomation.Size{Width: metadata.GlobalRect.Width, Height: metadata.GlobalRect.Height},
	}

	type queueItem struct {
		parent int
		depth  int
		el     *axuiautomation.Element
	}
	queue := []queueItem{{parent: -1, depth: 0, el: window}}
	elements := make(map[int]*axuiautomation.Element)
	nodes := make(map[int]computeruse.ElementNode)
	tree := make([]computeruse.ElementNode, 0, 128)
	index := 0
	complete := false
	defer func() {
		if complete {
			return
		}
		for _, el := range elements {
			if el != window {
				el.Release()
			}
		}
		for _, item := range queue {
			if item.el != nil && item.el != window {
				item.el.Release()
			}
		}
	}()

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if item.el == nil {
			continue
		}
		if err := boundAXTimeout(ctx, item.el); err != nil {
			if item.el != window {
				item.el.Release()
			}
			return computeruse.AppState{}, nil, nil, err
		}
		node := snapshotNode(item.el, item.parent, index, frame)
		tree = append(tree, node)
		nodes[index] = node
		elements[index] = item.el
		index++
		for _, child := range item.el.Children() {
			if child == nil {
				continue
			}
			queue = append(queue, queueItem{parent: node.Index, depth: item.depth + 1, el: child})
		}
	}

	state := computeruse.AppState{
		App:                app,
		ScreenshotMetadata: metadata,
		Window: computeruse.WindowInfo{
			WindowID:         metadata.TargetWindow,
			Title:            window.Title(),
			X:                int(math.Round(frame.Origin.X)),
			Y:                int(math.Round(frame.Origin.Y)),
			Width:            int(math.Round(frame.Size.Width)),
			Height:           int(math.Round(frame.Size.Height)),
			ScreenshotWidth:  metadata.Width,
			ScreenshotHeight: metadata.Height,
		},
		Tree:                tree,
		ScreenshotPNGBase64: base64.StdEncoding.EncodeToString(png),
		Approval:            computeruse.ApprovalState{Approved: true},
		Permissions: computeruse.PermissionState{
			AccessibilityGranted:   true,
			AccessibilityStatus:    "granted",
			ScreenRecordingGranted: true,
			ScreenRecordingStatus:  "granted",
		},
	}
	if instructions != nil {
		state.Instructions = instructions.Instructions(app)
	}
	if err := ctx.Err(); err != nil {
		return computeruse.AppState{}, nil, nil, err
	}
	if err := CheckScreenshotGeometry(ctx, window, metadata); err != nil {
		return computeruse.AppState{}, nil, nil, err
	}

	if err := ctx.Err(); err != nil {
		return computeruse.AppState{}, nil, nil, err
	}
	complete = true
	return state, elements, nodes, nil
}

func snapshotNode(el *axuiautomation.Element, parentIndex, index int, windowFrame axuiautomation.Rect) computeruse.ElementNode {
	frame := el.Frame()
	x := int(math.Round(frame.Origin.X - windowFrame.Origin.X))
	y := int(math.Round(frame.Origin.Y - windowFrame.Origin.Y))
	role := strings.TrimSpace(el.Role())
	value := el.Value()
	if value == "" && (role == "AXCheckBox" || role == "AXSwitch" || role == "AXRadioButton") {
		if el.IsChecked() {
			value = "1"
		} else {
			value = "0"
		}
	}
	return computeruse.ElementNode{
		Index:            index,
		ParentIndex:      parentIndex,
		Role:             role,
		Title:            el.Title(),
		Value:            value,
		Description:      strings.TrimSpace(el.Description()),
		Identifier:       el.Identifier(),
		X:                x,
		Y:                y,
		Width:            int(math.Round(frame.Size.Width)),
		Height:           int(math.Round(frame.Size.Height)),
		Enabled:          el.IsEnabled(),
		Settable:         isSettableRole(role),
		SecondaryActions: actionNames(el),
	}
}

func actionNames(el *axuiautomation.Element) []string {
	if el == nil || axCopyActionNames == nil {
		return nil
	}
	lib, err := cfhandle.Open()
	if err != nil {
		return nil
	}
	var names uintptr
	if axCopyActionNames(el.Ref(), &names) != 0 || names == 0 {
		return nil
	}
	defer lib.Release(names)

	count := corefoundation.CFArrayGetCount(corefoundation.CFArrayRef(names))
	out := make([]string, 0, count)
	for i := range count {
		ptr := corefoundation.CFArrayGetValueAtIndex(corefoundation.CFArrayRef(names), i)
		name := cfStringToGo(corefoundation.CFStringRef(uintptr(ptr)))
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func cfStringToGo(ref corefoundation.CFStringRef) string {
	if ref == 0 {
		return ""
	}
	buf := make([]byte, 1024)
	if !corefoundation.CFStringGetCString(ref, &buf[0], len(buf), uint32(corefoundation.KCFStringEncodingUTF8)) {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

func isSettableRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "AXComboBox", "AXSearchField", "AXSlider", "AXTextArea", "AXTextField", "AXValueIndicator":
		return true
	default:
		return false
	}
}

func selectWindow(app *axuiautomation.Application, title string) (*axuiautomation.Element, error) {
	if app == nil {
		return nil, fmt.Errorf("nil application")
	}
	if title != "" {
		if win := app.WindowByTitleContains(title); win != nil {
			return win, nil
		}
	}
	if win := app.MainWindow(); win != nil {
		if title == "" || strings.Contains(strings.ToLower(win.Title()), strings.ToLower(title)) {
			return win, nil
		}
		win.Release()
	}
	for _, win := range app.WindowList() {
		if win == nil {
			continue
		}
		if title == "" || strings.Contains(strings.ToLower(win.Title()), strings.ToLower(title)) {
			return win, nil
		}
		win.Release()
	}
	return nil, fmt.Errorf("no matching window found")
}

func openApp(ctx context.Context, selector string) (*axuiautomation.Application, computeruse.AppInfo, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, computeruse.AppInfo{}, fmt.Errorf("app is required")
	}
	if pid, err := strconv.Atoi(selector); err == nil {
		app := axuiautomation.NewApplicationFromPID(int32(pid))
		if app == nil {
			return nil, computeruse.AppInfo{}, fmt.Errorf("cannot connect to pid %d", pid)
		}
		setAXTimeout(app)
		axuiautomation.SpinRunLoop(200 * time.Millisecond)
		info := lookupAppInfo(ctx, app.PID(), app.BundleID(), selector)
		return app, info, nil
	}
	if app, err := axuiautomation.NewApplication(selector); err == nil {
		setAXTimeout(app)
		axuiautomation.SpinRunLoop(200 * time.Millisecond)
		info := lookupAppInfo(ctx, app.PID(), app.BundleID(), selector)
		return app, info, nil
	}
	apps, err := macosapp.ListRunningApps(ctx)
	if err != nil {
		return nil, computeruse.AppInfo{}, err
	}
	for _, candidate := range apps {
		if strings.EqualFold(candidate.Name, selector) || strings.Contains(strings.ToLower(candidate.Name), strings.ToLower(selector)) {
			app := axuiautomation.NewApplicationFromPID(int32(candidate.PID))
			if app == nil {
				continue
			}
			setAXTimeout(app)
			axuiautomation.SpinRunLoop(200 * time.Millisecond)
			return app, computeruse.AppInfo{
				Name:     candidate.Name,
				BundleID: candidate.BundleID,
				PID:      candidate.PID,
			}, nil
		}
	}
	return nil, computeruse.AppInfo{}, fmt.Errorf("app %q not found", selector)
}

func lookupAppInfo(ctx context.Context, pid int32, bundleID, selector string) computeruse.AppInfo {
	apps, err := macosapp.ListRunningApps(ctx)
	if err == nil {
		for _, app := range apps {
			if app.PID == int(pid) || (bundleID != "" && app.BundleID == bundleID) {
				return computeruse.AppInfo{
					Name:     app.Name,
					BundleID: app.BundleID,
					PID:      app.PID,
				}
			}
		}
	}
	return computeruse.AppInfo{
		Name:     selector,
		BundleID: bundleID,
		PID:      int(pid),
	}
}

func setAXTimeout(app *axuiautomation.Application) {
	if axSetMessagingTimeout == nil || app == nil {
		return
	}
	root := app.Root()
	if root == nil {
		return
	}
	axSetMessagingTimeout(root.Ref(), axTimeout)
}
