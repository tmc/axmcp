package winstate

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/coords"
)

type inputRunner func(context.Context, inputAction) error
type automationActionRunner func(context.Context, automationAction) error

type inputActionKind int

const (
	wheelDelta = 120
)

const (
	inputClick inputActionKind = iota + 1
	inputDrag
	inputKey
	inputText
	inputScroll
)

type inputAction struct {
	Kind       inputActionKind
	Target     uintptr
	Foreground bool
	Button     mouseButton
	ClickCount int
	Start      coords.ScreenPoint
	End        coords.ScreenPoint
	Key        string
	Text       string
	WheelDelta int
	WheelCount int
	Horizontal bool
}

type mouseButton int

const (
	mouseLeft mouseButton = iota + 1
	mouseRight
	mouseMiddle
)

type automationActionKind int

const (
	automationInvoke automationActionKind = iota + 1
	automationToggle
	automationSelect
	automationExpand
	automationCollapse
	automationExpandCollapse
	automationSetValue
)

type automationAction struct {
	Kind    automationActionKind
	Element uintptr
	Value   string
}

func (b Backend) ClickElement(ctx context.Context, snapshot computeruse.Snapshot, index int, opts computeruse.ClickOptions) error {
	s, err := windowsSnapshot(snapshot)
	if err != nil {
		return err
	}
	native, node, err := s.NativeElement(index)
	if err != nil {
		return err
	}
	if node.Width <= 0 || node.Height <= 0 {
		return fmt.Errorf("element has empty bounds")
	}
	if canInvokeElement(node, native, opts) {
		return b.runAutomationAction(ctx, automationAction{Kind: automationInvoke, Element: native.AutomationHandle})
	}
	target := native.WindowHandle
	if target == 0 {
		target = s.window.Handle
	}
	local := coords.Point{
		X: node.X + node.Width/2,
		Y: node.Y + node.Height/2,
	}
	if opts.ForegroundHID {
		return b.clickWindowLocal(ctx, s, s.window.Handle, local, opts)
	}
	return b.clickWindowLocal(ctx, s, target, local, opts)
}

func (b Backend) ClickPoint(ctx context.Context, snapshot computeruse.Snapshot, point computeruse.Point, opts computeruse.ClickOptions) error {
	s, err := windowsSnapshot(snapshot)
	if err != nil {
		return err
	}
	local, err := coords.ScreenshotPointToWindowLocal(s.state.Window, point.X, point.Y)
	if err != nil {
		return err
	}
	return b.clickWindowLocal(ctx, s, s.window.Handle, local, opts)
}

func (b Backend) Drag(ctx context.Context, snapshot computeruse.Snapshot, start, end computeruse.Point, opts computeruse.DragOptions) error {
	s, err := windowsSnapshot(snapshot)
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
	button, err := parseMouseButton(opts.Button)
	if err != nil {
		return err
	}
	startScreen, err := coords.WindowLocalToScreen(s.state.Window, startLocal)
	if err != nil {
		return err
	}
	endScreen, err := coords.WindowLocalToScreen(s.state.Window, endLocal)
	if err != nil {
		return err
	}
	return b.runInput(ctx, inputAction{
		Kind:   inputDrag,
		Target: s.window.Handle,
		Button: button,
		Start:  startScreen,
		End:    endScreen,
	})
}

func (b Backend) ScrollElement(ctx context.Context, snapshot computeruse.Snapshot, index int, opts computeruse.ScrollOptions) error {
	s, err := windowsSnapshot(snapshot)
	if err != nil {
		return err
	}
	native, node, err := s.NativeElement(index)
	if err != nil {
		return err
	}
	if node.Width <= 0 || node.Height <= 0 {
		return fmt.Errorf("element has empty bounds")
	}
	target := native.WindowHandle
	if target == 0 {
		target = s.window.Handle
	}
	local := coords.Point{
		X: node.X + node.Width/2,
		Y: node.Y + node.Height/2,
	}
	screen, err := coords.WindowLocalToScreen(s.state.Window, local)
	if err != nil {
		return err
	}
	delta, count, horizontal, err := windowsScroll(opts)
	if err != nil {
		return err
	}
	return b.runInput(ctx, inputAction{
		Kind:       inputScroll,
		Target:     target,
		Start:      screen,
		WheelDelta: delta,
		WheelCount: count,
		Horizontal: horizontal,
	})
}

func (b Backend) PerformSecondaryAction(ctx context.Context, snapshot computeruse.Snapshot, index int, action string) error {
	s, err := windowsSnapshot(snapshot)
	if err != nil {
		return err
	}
	native, _, err := s.NativeElement(index)
	if err != nil {
		return err
	}
	if native.AutomationHandle == 0 {
		return computeruse.PlatformUnsupported("perform UI Automation action")
	}
	kind, err := parseAutomationAction(action)
	if err != nil {
		return err
	}
	return b.runAutomationAction(ctx, automationAction{Kind: kind, Element: native.AutomationHandle})
}

func (b Backend) SetValue(ctx context.Context, snapshot computeruse.Snapshot, index int, value string) error {
	s, err := windowsSnapshot(snapshot)
	if err != nil {
		return err
	}
	native, node, err := s.NativeElement(index)
	if err != nil {
		return err
	}
	if native.AutomationHandle != 0 {
		err := b.runAutomationAction(ctx, automationAction{Kind: automationSetValue, Element: native.AutomationHandle, Value: value})
		if err == nil {
			return nil
		}
		if !errors.Is(err, computeruse.ErrPlatformUnsupported) {
			return err
		}
	}
	return b.setValueByKeyboard(ctx, s, native, node, value)
}

func (b Backend) PressKey(ctx context.Context, snapshot computeruse.Snapshot, key string) error {
	s, err := windowsSnapshot(snapshot)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("missing key")
	}
	return b.runInput(ctx, inputAction{Kind: inputKey, Target: s.window.Handle, Key: key})
}

func (b Backend) TypeText(ctx context.Context, snapshot computeruse.Snapshot, elementIndex *int, text string) error {
	s, err := windowsSnapshot(snapshot)
	if err != nil {
		return err
	}
	target := s.window.Handle
	if elementIndex != nil {
		native, _, err := s.NativeElement(*elementIndex)
		if err != nil {
			return err
		}
		if native.WindowHandle != 0 {
			target = native.WindowHandle
		}
	}
	return b.runInput(ctx, inputAction{Kind: inputText, Target: target, Text: text})
}

func (b Backend) clickWindowLocal(ctx context.Context, s *Snapshot, target uintptr, local coords.Point, opts computeruse.ClickOptions) error {
	button, err := parseMouseButton(opts.Button)
	if err != nil {
		return err
	}
	screen, err := coords.WindowLocalToScreen(s.state.Window, local)
	if err != nil {
		return err
	}
	return b.runInput(ctx, inputAction{
		Kind:       inputClick,
		Target:     target,
		Foreground: opts.ForegroundHID,
		Button:     button,
		ClickCount: normalizeClickCount(opts.ClickCount),
		Start:      screen,
	})
}

func (b Backend) setValueByKeyboard(ctx context.Context, s *Snapshot, native NativeElement, node computeruse.ElementNode, value string) error {
	if !node.Settable {
		return computeruse.PlatformUnsupported("set value with UI Automation")
	}
	if node.Width <= 0 || node.Height <= 0 {
		return fmt.Errorf("element has empty bounds")
	}
	target := native.WindowHandle
	if target == 0 {
		target = s.window.Handle
	}
	local := coords.Point{X: node.X + node.Width/2, Y: node.Y + node.Height/2}
	if err := b.clickWindowLocal(ctx, s, target, local, computeruse.ClickOptions{}); err != nil {
		return err
	}
	if err := b.runInput(ctx, inputAction{Kind: inputKey, Target: target, Key: "ctrl+a"}); err != nil {
		return err
	}
	if value == "" {
		return b.runInput(ctx, inputAction{Kind: inputKey, Target: target, Key: "BackSpace"})
	}
	return b.runInput(ctx, inputAction{Kind: inputText, Target: target, Text: value})
}

func (b Backend) runInput(ctx context.Context, action inputAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if action.Target == 0 {
		return fmt.Errorf("missing window handle")
	}
	run := b.input
	if run == nil {
		run = sendWindowInput
	}
	return run(ctx, action)
}

func (b Backend) runAutomationAction(ctx context.Context, action automationAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if action.Element == 0 {
		return computeruse.PlatformUnsupported("perform UI Automation action")
	}
	run := b.uiaAction
	if run == nil {
		run = performAutomationAction
	}
	return run(ctx, action)
}

func windowsSnapshot(snapshot computeruse.Snapshot) (*Snapshot, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("missing state snapshot")
	}
	s, ok := snapshot.(*Snapshot)
	if !ok {
		return nil, fmt.Errorf("state snapshot is not from windows backend")
	}
	return s, nil
}

func canInvokeElement(node computeruse.ElementNode, native NativeElement, opts computeruse.ClickOptions) bool {
	if opts.ForegroundHID {
		return false
	}
	if native.AutomationHandle == 0 || normalizeClickCount(opts.ClickCount) != 1 {
		return false
	}
	if strings.TrimSpace(opts.Button) != "" && !strings.EqualFold(strings.TrimSpace(opts.Button), "left") {
		return false
	}
	for _, action := range node.SecondaryActions {
		if strings.EqualFold(strings.TrimSpace(action), "invoke") {
			return true
		}
	}
	return false
}

func normalizeClickCount(clickCount int) int {
	if clickCount < 1 {
		return 1
	}
	return clickCount
}

func windowsScroll(opts computeruse.ScrollOptions) (delta, count int, horizontal bool, err error) {
	switch strings.ToLower(strings.TrimSpace(opts.Direction)) {
	case "", "down":
		delta = -wheelDelta
	case "up":
		delta = wheelDelta
	case "left":
		delta = -wheelDelta
		horizontal = true
	case "right":
		delta = wheelDelta
		horizontal = true
	default:
		return 0, 0, false, fmt.Errorf("invalid scroll direction %q; use up, down, left, or right", opts.Direction)
	}
	pages := opts.Pages
	if pages == 0 {
		pages = 1
	}
	if pages < 0 {
		pages = -pages
	}
	count = int(math.Round(pages * 5))
	if count < 1 {
		count = 1
	}
	return delta, count, horizontal, nil
}

func parseAutomationAction(action string) (automationActionKind, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "invoke", "press", "click":
		return automationInvoke, nil
	case "toggle":
		return automationToggle, nil
	case "select":
		return automationSelect, nil
	case "expand":
		return automationExpand, nil
	case "collapse":
		return automationCollapse, nil
	case "expand_collapse", "expandcollapse":
		return automationExpandCollapse, nil
	default:
		return 0, fmt.Errorf("unsupported UI Automation action %q", action)
	}
}

func parseMouseButton(button string) (mouseButton, error) {
	switch strings.ToLower(strings.TrimSpace(button)) {
	case "", "left":
		return mouseLeft, nil
	case "right":
		return mouseRight, nil
	case "middle":
		return mouseMiddle, nil
	default:
		return 0, fmt.Errorf("invalid button %q; use left, right, or middle", button)
	}
}
