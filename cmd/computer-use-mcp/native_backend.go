package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/ebitengine/purego"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/appstate"
	"github.com/tmc/axmcp/internal/computeruse/input"
	"github.com/tmc/axmcp/internal/computeruse/session"
	"golang.org/x/sys/unix"
)

type nativeOSBackend struct{ rt *runtimeState }

func (b *nativeOSBackend) Resolve(ctx context.Context, selector string) (computeruse.AppInfo, error) {
	apps, err := appstate.ListApps(ctx)
	if err != nil {
		return computeruse.AppInfo{}, err
	}
	return exactNativeApp(apps, selector)
}
func exactNativeApp(apps []computeruse.AppInfo, selector string) (computeruse.AppInfo, error) {
	selector = strings.TrimSpace(selector)
	var found computeruse.AppInfo
	count := 0
	for _, app := range apps {
		if selector != "" && (selector == strconv.Itoa(app.PID) || strings.EqualFold(selector, app.BundleID) || strings.EqualFold(selector, app.Name)) {
			found = app
			count++
		}
	}
	if count != 1 || found.PID <= 0 {
		return computeruse.AppInfo{}, fmt.Errorf("app must match exactly one running process")
	}
	return found, nil
}
func (b *nativeOSBackend) Authorize(ctx context.Context, req *mcp.CallToolRequest, app computeruse.AppInfo, observe bool) (computeruse.PermissionState, computeruse.ApprovalState, error) {
	p := currentPermissions()
	if b.rt == nil || b.rt.approvals == nil {
		return p, computeruse.ApprovalState{}, fmt.Errorf("app approval store unavailable")
	}
	a := b.rt.approvals.Status(app.BundleID)
	if err := ctx.Err(); err != nil {
		return p, a, err
	}
	if p.Pending {
		return p, a, nil
	}
	if observe && a.Required && !a.Approved {
		var err error
		a, err = elicitApproval(ctx, req, b.rt, app)
		if err != nil {
			return p, a, err
		}
	}
	if !observe && b.rt.intervention != nil {
		if _, blocked := b.rt.intervention.Blocked(time.Now()); blocked {
			return p, a, fmt.Errorf("native action paused by physical user input")
		}
	}
	return p, a, nil
}
func nativeProcessStart(pid int) (nativeStart, error) {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return nativeStart{}, fmt.Errorf("process identity: %w", err)
	}
	if p == nil || p.Proc.P_pid != int32(pid) || p.Proc.P_starttime.Sec == 0 {
		return nativeStart{}, fmt.Errorf("process identity unavailable")
	}
	return nativeStart{Seconds: p.Proc.P_starttime.Sec, Microseconds: int64(p.Proc.P_starttime.Usec)}, nil
}
func (b *nativeOSBackend) Capture(ctx context.Context, app computeruse.AppInfo, window uint32) (session.Snapshot, nativeTarget, error) {
	return b.capture(ctx, app, window, nil, nil)
}
func (b *nativeOSBackend) capture(ctx context.Context, app computeruse.AppInfo, window uint32, element *axuiautomation.Element, expectedStart *nativeStart) (session.Snapshot, nativeTarget, error) {
	var target nativeTarget
	if err := ctx.Err(); err != nil {
		return nil, target, err
	}
	before, err := nativeProcessStart(app.PID)
	if err != nil {
		return nil, target, err
	}
	if expectedStart != nil && before != *expectedStart {
		return nil, target, fmt.Errorf("process changed since discovery")
	}
	if b.rt == nil || b.rt.builder == nil {
		return nil, target, fmt.Errorf("native snapshot builder unavailable")
	}
	var snapshot *appstate.Snapshot
	if element != nil {
		snapshot, err = b.rt.builder.BuildWindowElement(ctx, int32(app.PID), element, b.rt.instructions)
	} else {
		snapshot, err = b.rt.builder.BuildWindow(ctx, int32(app.PID), window, b.rt.instructions)
	}
	if err != nil {
		return nil, target, err
	}
	fail := func(err error) (session.Snapshot, nativeTarget, error) {
		snapshot.Close()
		return nil, nativeTarget{}, err
	}
	after, err := nativeProcessStart(app.PID)
	if err != nil {
		return fail(err)
	}
	state := snapshot.State()
	if before != after || state.App.PID != app.PID {
		return fail(fmt.Errorf("process changed during observation"))
	}
	if state.Window.WindowID == 0 || (window != 0 && state.Window.WindowID != window) {
		return fail(fmt.Errorf("window changed during observation"))
	}
	root, _, err := snapshot.Resolve(0)
	if err != nil {
		return fail(err)
	}
	if err := nativeAXBudget(ctx, root); err != nil {
		return fail(err)
	}
	if !sameNativeWindow(state.Window, root) {
		return fail(fmt.Errorf("window moved during observation"))
	}
	return snapshot, nativeTarget{App: app, Start: after, Window: state.Window}, nil
}
func nativeAXBudget(ctx context.Context, el *axuiautomation.Element) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if el == nil {
		return fmt.Errorf("native element unavailable")
	}
	seconds := 5.0
	if deadline, ok := ctx.Deadline(); ok {
		seconds = math.Min(seconds, time.Until(deadline).Seconds())
	}
	if seconds <= 0 {
		return context.DeadlineExceeded
	}
	if code := axuiautomation.AXUIElementSetMessagingTimeout(el.Ref(), float32(seconds)); code != 0 {
		return fmt.Errorf("set native timeout: %d", code)
	}
	return nil
}
func sameNativeWindow(w computeruse.WindowInfo, root *axuiautomation.Element) bool {
	current, exists := readNativeWindow(root)
	return exists && sameNativeWindowInfo(w, current)
}

func readNativeWindow(root *axuiautomation.Element) (computeruse.WindowInfo, bool) {
	if root == nil || !root.Exists() {
		return computeruse.WindowInfo{}, false
	}
	f := root.Frame()
	return computeruse.WindowInfo{WindowID: root.WindowID(), X: int(math.Round(f.Origin.X)), Y: int(math.Round(f.Origin.Y)), Width: int(math.Round(f.Size.Width)), Height: int(math.Round(f.Size.Height))}, true
}

func nativeFocusedWindow(ctx context.Context, app *axuiautomation.Application) (uint32, error) {
	if app == nil {
		return 0, fmt.Errorf("native app unavailable")
	}
	el := app.FocusedElement()
	for depth := 0; el != nil && depth < 64; depth++ {
		if err := nativeAXBudget(ctx, el); err != nil {
			el.Release()
			return 0, err
		}
		id := el.WindowID()
		if id != 0 {
			el.Release()
			return id, nil
		}
		parent := el.Parent()
		el.Release()
		el = parent
	}
	if el != nil {
		el.Release()
	}
	return 0, fmt.Errorf("focused window unavailable")
}

// Direct AX operations address their retained element, even in a background
// window. PID-directed events do not carry a window ID and still need focus.
func checkNativeFocus(action string, windowID uint32, focused func() (uint32, error)) error {
	switch action {
	case "click", "drag", "set_value", "secondary_action":
		return nil
	}
	id, err := focused()
	if err != nil {
		return err
	}
	if id != windowID {
		return fmt.Errorf("focused window changed; call native_observe")
	}
	return nil
}

func (b *nativeOSBackend) Check(ctx context.Context, target nativeTarget, lease *session.Lease, in nativeActInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	start, err := nativeProcessStart(target.App.PID)
	if err != nil {
		return err
	}
	if start != target.Start {
		return fmt.Errorf("native process instance changed")
	}
	root, _, err := lease.Resolve(0)
	if err != nil {
		return err
	}
	if err := nativeAXBudget(ctx, root); err != nil {
		return err
	}
	if !sameNativeWindow(target.Window, root) {
		return fmt.Errorf("observed window disappeared or moved")
	}
	if nativeHasCoordinates(in) {
		if err := appstate.CheckScreenshotGeometry(ctx, root, lease.State().ScreenshotMetadata); err != nil {
			return err
		}
	}

	if err := checkNativeFocus(in.Action, target.Window.WindowID, func() (uint32, error) {
		return nativeFocusedWindow(ctx, root.Application())
	}); err != nil {
		return err
	}
	if in.ElementIndex != nil {
		el, node, err := lease.Resolve(*in.ElementIndex)
		if err != nil {
			return err
		}
		if err := nativeAXBudget(ctx, el); err != nil {
			return err
		}
		if !el.Exists() || !el.IsEnabled() || el.WindowID() != target.Window.WindowID || el.Role() != node.Role || el.Identifier() != node.Identifier {
			return fmt.Errorf("observed element is stale or disabled")
		}
		if in.Action == "set_value" && !el.IsAttributeSettable("AXValue") {
			return fmt.Errorf("observed value is not settable")
		}
		if in.Action == "secondary_action" {
			allowed := false
			for _, name := range node.SecondaryActions {
				if name == in.SecondaryAction {
					allowed = true
				}
			}
			if !allowed {
				return fmt.Errorf("secondary action was not advertised")
			}
		}
	}
	// Refresh policy inputs from the live tree, without borrowing its handles for
	// dispatch. The action continues to use only its retained observation handles.
	current, _, err := b.Capture(ctx, target.App, target.Window.WindowID)
	if err != nil {
		return fmt.Errorf("current policy observation: %w", err)
	}
	defer current.Close()
	if b.rt.urlPolicy != nil {
		if err := b.rt.urlPolicy.CheckState(current.State()); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func (b *nativeOSBackend) Perform(ctx context.Context, target nativeTarget, lease *session.Lease, in nativeActInput) (bool, error) {
	checks := nativeDispatchGuard{
		authorize: func() (computeruse.PermissionState, computeruse.ApprovalState, error) {
			return b.Authorize(ctx, nil, target.App, false)
		},
		processStart: func() (nativeStart, error) { return nativeProcessStart(target.App.PID) },
		window: func() (computeruse.WindowInfo, bool, error) {
			root, _, err := lease.Resolve(0)
			if err != nil {
				return computeruse.WindowInfo{}, false, err
			}
			if err := nativeAXBudget(ctx, root); err != nil {
				return computeruse.WindowInfo{}, false, err
			}
			window, exists := readNativeWindow(root)
			return window, exists, nil
		},
		focusedWindow: func() (uint32, error) {
			root, _, err := lease.Resolve(0)
			if err != nil {
				return 0, err
			}
			return nativeFocusedWindow(ctx, root.Application())
		},
	}
	guard := func() error {
		if err := checks.check(ctx, target, in.Action); err != nil {
			return err
		}
		if nativeHasCoordinates(in) {
			root, _, err := lease.Resolve(0)
			if err != nil {
				return err
			}
			return appstate.CheckScreenshotGeometry(ctx, root, lease.State().ScreenshotMetadata)
		}
		return nil
	}
	if err := guard(); err != nil {
		return false, err
	}
	if nativeHasCoordinates(in) {
		return performNativePointer(ctx, target, lease, in, guard)
	}
	var el *axuiautomation.Element
	if in.ElementIndex != nil {
		var err error
		el, _, err = lease.Resolve(*in.ElementIndex)
		if err != nil {
			return false, err
		}
		if err := nativeAXBudget(ctx, el); err != nil {
			return false, err
		}
	}
	switch in.Action {
	case "click":
		return true, el.PerformAction("AXPress")
	case "set_value":
		return true, el.SetValue(*in.Value)
	case "secondary_action":
		return true, el.PerformAction(in.SecondaryAction)
	case "scroll":
		if err := prepareNativeEvents(); err != nil {
			return false, err
		}
		lines := int32(math.Max(1, math.Round(in.Pages*12)))
		var x, y int32
		switch in.Direction {
		case "up":
			y = lines
		case "down":
			y = -lines
		case "left":
			x = lines
		case "right":
			x = -lines
		}
		event := coregraphics.CGEventCreateScrollWheelEvent2(0, coregraphics.KCGScrollEventUnitLine, 2, y, x, 0)
		if event == 0 {
			return false, fmt.Errorf("create native scroll event")
		}
		defer nativeKeyRelease.release(uintptr(event))
		xPos, yPos := el.Center()
		coregraphics.CGEventSetLocation(event, corefoundation.CGPoint{X: float64(xPos), Y: float64(yPos)})
		if err := guard(); err != nil {
			return false, err
		}
		coregraphics.CGEventPostToPid(int32(target.App.PID), event)
		return true, nil
	case "type_text":
		if err := el.Focus(); err != nil {
			return true, err
		}
		focusedChecks := checks
		focusedChecks.elementFocused = func() (bool, error) {
			if err := nativeAXBudget(ctx, el); err != nil {
				return false, err
			}
			return el.IsFocused(), nil
		}
		focusedGuard := func() error { return focusedChecks.check(ctx, target, in.Action) }
		_, err := nativeKeySequence(ctx, target, in.Text, nil, focusedGuard, postNativeKey)
		return true, err
	case "press_key":
		combo, err := input.ParseKeyCombo(in.Key)
		if err != nil {
			return false, err
		}
		return nativeKeySequence(ctx, target, "", &combo, guard, postNativeKey)
	}
	return false, fmt.Errorf("unsupported native action")
}

type nativeKeyPost func(context.Context, nativeTarget, bool, uint16, coregraphics.CGEventFlags, []uint16) (bool, error)

func nativeKeySequence(ctx context.Context, target nativeTarget, text string, combo *input.KeyCombo, guard func() error, post nativeKeyPost) (bool, error) {
	attempted := false
	run := func(code uint16, flags coregraphics.CGEventFlags, units []uint16) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := guard(); err != nil {
			return err
		}
		sent, err := post(ctx, target, true, code, flags, units)
		attempted = attempted || sent
		if sent {
			// Pair our own key-down even on cancellation; the production poster checks
			// the original process instance before every post, including this cleanup.
			_, upErr := post(ctx, target, false, code, flags, units)
			err = errors.Join(err, upErr)
		}
		return err
	}
	if combo != nil {
		var flags coregraphics.CGEventFlags
		if combo.Shift {
			flags |= coregraphics.KCGEventFlagMaskShift
		}
		if combo.Control {
			flags |= coregraphics.KCGEventFlagMaskControl
		}
		if combo.Option {
			flags |= coregraphics.KCGEventFlagMaskAlternate
		}
		if combo.Command {
			flags |= coregraphics.KCGEventFlagMaskCommand
		}
		returnAttemptErr := run(combo.KeyCode, flags, nil)
		return attempted, returnAttemptErr
	}
	for _, r := range text {
		if err := run(0, 0, utf16.Encode([]rune{r})); err != nil {
			return attempted, err
		}
	}
	return attempted, nil
}

var nativeKeyRelease struct {
	sync.Once
	release func(uintptr)
	err     error
}

func postNativeKey(ctx context.Context, target nativeTarget, down bool, code uint16, flags coregraphics.CGEventFlags, units []uint16) (bool, error) {
	return postNativeKeyToInstance(ctx, target, down, code, flags, units, nativeProcessStart, emitNativeKey)
}

func postNativeKeyToInstance(ctx context.Context, target nativeTarget, down bool, code uint16, flags coregraphics.CGEventFlags, units []uint16, startOf func(int) (nativeStart, error), post nativeKeyPost) (bool, error) {
	start, err := startOf(target.App.PID)
	if err != nil {
		return false, err
	}
	if start != target.Start {
		return false, fmt.Errorf("native process changed before keyboard post")
	}
	// Cancellation blocks new down events. Cleanup still pairs this action's
	// own down events if the original process instance is alive.
	if down {
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	return post(ctx, target, down, code, flags, units)
}

func emitNativeKey(ctx context.Context, target nativeTarget, down bool, code uint16, flags coregraphics.CGEventFlags, units []uint16) (bool, error) {
	if err := prepareNativeEvents(); err != nil {
		return false, err
	}
	event := coregraphics.CGEventCreateKeyboardEvent(0, code, down)
	if event == 0 {
		return false, fmt.Errorf("create native key event")
	}
	defer nativeKeyRelease.release(uintptr(event))
	coregraphics.CGEventSetFlags(event, flags)
	if len(units) > 0 {
		coregraphics.CGEventKeyboardSetUnicodeString(event, uint(len(units)), &units[0])
	}
	if down {
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	coregraphics.CGEventPostToPid(int32(target.App.PID), event)
	return true, nil
}

func prepareNativeEvents() error {
	nativeKeyRelease.Do(func() {
		var lib uintptr
		lib, nativeKeyRelease.err = purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if nativeKeyRelease.err == nil {
			purego.RegisterLibFunc(&nativeKeyRelease.release, lib, "CFRelease")
		}
	})
	if nativeKeyRelease.err != nil {
		return nativeKeyRelease.err
	}

	return nil
}
