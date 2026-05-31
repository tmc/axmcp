package winstate

import (
	"context"
	"fmt"
	"strings"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/coords"
)

type inputRunner func(context.Context, inputAction) error

type inputActionKind int

const (
	inputClick inputActionKind = iota + 1
	inputDrag
)

type inputAction struct {
	Kind       inputActionKind
	Target     uintptr
	Button     mouseButton
	ClickCount int
	Start      coords.ScreenPoint
	End        coords.ScreenPoint
}

type mouseButton int

const (
	mouseLeft mouseButton = iota + 1
	mouseRight
	mouseMiddle
)

func (b Backend) ClickElement(ctx context.Context, snapshot computeruse.Snapshot, index int, opts computeruse.ClickOptions) error {
	if opts.ForegroundHID {
		return computeruse.PlatformUnsupported("foreground SendInput click")
	}
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
	return b.clickWindowLocal(ctx, s, target, local, opts)
}

func (b Backend) ClickPoint(ctx context.Context, snapshot computeruse.Snapshot, point computeruse.Point, opts computeruse.ClickOptions) error {
	if opts.ForegroundHID {
		return computeruse.PlatformUnsupported("foreground SendInput click")
	}
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

func (b Backend) ScrollElement(context.Context, computeruse.Snapshot, int, computeruse.ScrollOptions) error {
	return computeruse.PlatformUnsupported("scroll element with UI Automation")
}

func (b Backend) PerformSecondaryAction(context.Context, computeruse.Snapshot, int, string) error {
	return computeruse.PlatformUnsupported("perform UI Automation action")
}

func (b Backend) SetValue(context.Context, computeruse.Snapshot, int, string) error {
	return computeruse.PlatformUnsupported("set value with UI Automation")
}

func (b Backend) PressKey(context.Context, computeruse.Snapshot, string) error {
	return computeruse.PlatformUnsupported("press key with Win32 messages")
}

func (b Backend) TypeText(context.Context, computeruse.Snapshot, *int, string) error {
	return computeruse.PlatformUnsupported("type text with Win32 messages")
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
		Button:     button,
		ClickCount: normalizeClickCount(opts.ClickCount),
		Start:      screen,
	})
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

func normalizeClickCount(clickCount int) int {
	if clickCount < 1 {
		return 1
	}
	return clickCount
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
