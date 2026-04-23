package appstate

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/png"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/ghostcursor"
	"github.com/tmc/axmcp/internal/macosapp"
)

const axTimeout = 5

var axSetMessagingTimeout func(element uintptr, timeoutInSeconds float32) int32

func init() {
	lib, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	purego.RegisterLibFunc(&axSetMessagingTimeout, lib, "AXUIElementSetMessagingTimeout")
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
	state, elements, nodes, err := buildState(info, window, instructions)
	if err != nil {
		window.Release()
		app.Close()
		return nil, err
	}
	owned := make([]*axuiautomation.Element, 0, len(elements))
	for _, el := range elements {
		owned = append(owned, el)
	}
	return &Snapshot{
		state:    state,
		app:      app,
		elements: elements,
		nodes:    nodes,
		owned:    owned,
	}, nil
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

func buildState(app computeruse.AppInfo, window *axuiautomation.Element, instructions computeruse.InstructionProvider) (computeruse.AppState, map[int]*axuiautomation.Element, map[int]computeruse.ElementNode, error) {
	frame := window.Frame()
	png, err := captureWindow(window)
	if err != nil {
		return computeruse.AppState{}, nil, nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(png))
	if err != nil {
		return computeruse.AppState{}, nil, nil, fmt.Errorf("decode screenshot: %w", err)
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

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if item.el == nil {
			continue
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
		App: app,
		Window: computeruse.WindowInfo{
			Title:            strings.TrimSpace(window.Title()),
			X:                int(math.Round(frame.Origin.X)),
			Y:                int(math.Round(frame.Origin.Y)),
			Width:            int(math.Round(frame.Size.Width)),
			Height:           int(math.Round(frame.Size.Height)),
			ScreenshotWidth:  cfg.Width,
			ScreenshotHeight: cfg.Height,
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
	return state, elements, nodes, nil
}

func snapshotNode(el *axuiautomation.Element, parentIndex, index int, windowFrame axuiautomation.Rect) computeruse.ElementNode {
	frame := el.Frame()
	x := int(math.Round(frame.Origin.X - windowFrame.Origin.X))
	y := int(math.Round(frame.Origin.Y - windowFrame.Origin.Y))
	role := strings.TrimSpace(el.Role())
	value := strings.TrimSpace(el.Value())
	if value == "" && (role == "AXCheckBox" || role == "AXSwitch" || role == "AXRadioButton") {
		if el.IsChecked() {
			value = "1"
		} else {
			value = "0"
		}
	}
	return computeruse.ElementNode{
		Index:       index,
		ParentIndex: parentIndex,
		Role:        role,
		Title:       strings.TrimSpace(el.Title()),
		Value:       value,
		Description: strings.TrimSpace(el.Description()),
		Identifier:  strings.TrimSpace(el.Identifier()),
		X:           x,
		Y:           y,
		Width:       int(math.Round(frame.Size.Width)),
		Height:      int(math.Round(frame.Size.Height)),
		Enabled:     el.IsEnabled(),
		Settable:    isSettableRole(role),
	}
}

func isSettableRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "AXComboBox", "AXSearchField", "AXSlider", "AXTextArea", "AXTextField", "AXValueIndicator":
		return true
	default:
		return false
	}
}

func captureWindow(window *axuiautomation.Element) ([]byte, error) {
	if window == nil {
		return nil, fmt.Errorf("nil window")
	}
	frame := window.Frame()
	if png, err := window.Screenshot(); err == nil && len(png) > 0 {
		ghostcursor.FlashCaptureRect(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: frame.Origin.X, Y: frame.Origin.Y},
			Size:   corefoundation.CGSize{Width: frame.Size.Width, Height: frame.Size.Height},
		})
		return png, nil
	}
	rectArg := fmt.Sprintf("%d,%d,%d,%d",
		int(frame.Origin.X),
		int(frame.Origin.Y),
		int(frame.Size.Width),
		int(frame.Size.Height),
	)
	f, err := os.CreateTemp("", "computer-use-window-*.png")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		os.Remove(name)
		return nil, fmt.Errorf("close temp file: %w", err)
	}
	defer os.Remove(name)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "screencapture", "-x", "-R", rectArg, "-t", "png", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("screencapture %s: %w: %s", rectArg, err, strings.TrimSpace(string(out)))
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read temp screenshot: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty screenshot")
	}
	ghostcursor.FlashCaptureRect(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: frame.Origin.X, Y: frame.Origin.Y},
		Size:   corefoundation.CGSize{Width: frame.Size.Width, Height: frame.Size.Height},
	})
	return data, nil
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
