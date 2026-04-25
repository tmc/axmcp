package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/ghostcursor"
)

const (
	defaultCursorGlide  = 280 * time.Millisecond
	defaultCursorSettle = 90 * time.Millisecond
	defaultCursorHold   = 200 * time.Millisecond
)

// cursorGlideDuration returns the animation duration used when moving the
// ghost cursor to a click target. Setting AXMCP_CURSOR_GLIDE_MS=0 reduces
// the glide to an instant overlay jump without the real system cursor warp
// that would otherwise accompany a legacy teleport.
func cursorGlideDuration() time.Duration {
	return envDurationMS("AXMCP_CURSOR_GLIDE_MS", defaultCursorGlide)
}

// cursorSettleDuration returns the pause inserted after the glide completes
// and before the synthetic click fires, giving the overlay a moment to land
// in its Pressed state. Tunable via AXMCP_CURSOR_SETTLE_MS; 0 disables.
func cursorSettleDuration() time.Duration {
	return envDurationMS("AXMCP_CURSOR_SETTLE_MS", defaultCursorSettle)
}

// cursorHoldDuration returns the pause inserted after the click completes so
// the overlay's idle-fade animation has time to play. Tunable via
// AXMCP_CURSOR_HOLD_MS; 0 disables.
func cursorHoldDuration() time.Duration {
	return envDurationMS("AXMCP_CURSOR_HOLD_MS", defaultCursorHold)
}

func envDurationMS(name string, fallback time.Duration) time.Duration {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return fallback
	}
	ms, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || ms < 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

// targetVisible reports whether el's owning app is currently the frontmost
// macOS application. The ghost cursor overlay is suppressed when it isn't,
// so the cursor doesn't appear to glide across whatever foreground window
// is occluding the click target. A nil element means "unknown" — caller
// hasn't given us enough to gate on, so we default to drawing the overlay.
func targetVisible(el *axuiautomation.Element) bool {
	if el == nil {
		return true
	}
	app := el.Application()
	if app == nil {
		return true
	}
	frontPID := int(appkit.GetNSWorkspaceClass().SharedWorkspace().FrontmostApplication().ProcessIdentifier())
	if frontPID <= 0 {
		return true
	}
	return frontPID == int(app.PID())
}

// glideCursorTo animates the ghost cursor to (x, y) before a click. It is a
// best-effort visual hint — errors and disabled cursors are silently ignored
// so callers can treat it as a no-op. When owner is non-nil and its app is
// not frontmost, the overlay is suppressed entirely (no glide, no flash).
func glideCursorTo(x, y int, owner *axuiautomation.Element) {
	if !ghostcursor.Enabled() || !targetVisible(owner) {
		return
	}
	primeGhostCursor()
	_ = ghostcursor.Default().MoveTo(context.Background(),
		ghostcursor.ScreenPosition(x, y),
		ghostcursor.MoveOptions{
			Duration:   cursorGlideDuration(),
			Activity:   ghostcursor.ActivityMoving,
			CurveStyle: ghostcursor.CurveBezier,
		})
}

var primeGhostOnce sync.Once

// primeGhostCursor shows the overlay at the current OS cursor position the
// first time it's called. Without this, MoveTo has no prior position and
// falls through to an instant Show at the target — which reads as a
// teleport for the first click in a process. Subsequent clicks glide from
// the previous position and don't need priming.
func primeGhostCursor() {
	primeGhostOnce.Do(func() {
		event := coregraphics.CGEventCreate(0)
		if event == 0 {
			return
		}
		defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))
		loc := coregraphics.CGEventGetLocation(event)
		_ = ghostcursor.Default().Show(
			ghostcursor.ScreenPosition(int(math.Round(loc.X)), int(math.Round(loc.Y))),
			ghostcursor.ActivityIdle,
			0,
		)
	})
}

// flashClickActivity transitions the ghost cursor into the Pressed state at
// (x, y). When owner is non-nil and not frontmost, the overlay stays hidden.
func flashClickActivity(x, y int, owner *axuiautomation.Element) {
	if !targetVisible(owner) {
		return
	}
	_ = ghostcursor.Default().Show(
		ghostcursor.ScreenPosition(x, y),
		ghostcursor.ActivityPressed,
		0,
	)
}

// settleClickActivity returns the ghost cursor to the Idle state after a
// click, holding the overlay on-screen at (x, y) so the cursor dims through
// its idle fade animation rather than snapping away between actions. When
// owner is non-nil and not frontmost the overlay stays hidden.
func settleClickActivity(x, y int, owner *axuiautomation.Element) {
	if !targetVisible(owner) {
		return
	}
	_ = ghostcursor.Default().Show(
		ghostcursor.ScreenPosition(x, y),
		ghostcursor.ActivityIdle,
		0,
	)
}

var (
	cgEventCreateMouseEvent     func(source uintptr, mouseType int32, x, y float64, button int32) uintptr
	cgEventCreateScrollWheelEvt func(source uintptr, units int32, wheelCount uint32, wheel1, wheel2, wheel3 int32) uintptr
	cgEventPost                 func(tap int32, event uintptr)
	cgEventSetIntegerValueField func(event uintptr, field uint32, value int64)
	cgWarpMouseCursorPosition   func(x, y float64) int32
	cgMouseEventsOnce           sync.Once
)

const (
	cgEventLeftMouseDown     = 1
	cgEventLeftMouseUp       = 2
	cgEventRightMouseDown    = 3
	cgEventRightMouseUp      = 4
	cgEventMouseMoved        = 5
	cgEventLeftMouseDragged  = 6
	cgEventRightMouseDragged = 7
	cgMouseEventClickState   = 1
	cgMouseButtonLeft        = 0
	cgMouseButtonRight       = 1
	cgHIDEventTap            = 0
)

func initCGMouseEvents() {
	cgMouseEventsOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&cgEventCreateMouseEvent, lib, "CGEventCreateMouseEvent")
		purego.RegisterLibFunc(&cgEventCreateScrollWheelEvt, lib, "CGEventCreateScrollWheelEvent")
		purego.RegisterLibFunc(&cgEventPost, lib, "CGEventPost")
		purego.RegisterLibFunc(&cgEventSetIntegerValueField, lib, "CGEventSetIntegerValueField")
		purego.RegisterLibFunc(&cgWarpMouseCursorPosition, lib, "CGWarpMouseCursorPosition")
	})
}

func localSize(el *axuiautomation.Element) (int, int) {
	frame := el.Frame()
	return int(math.Round(frame.Size.Width)), int(math.Round(frame.Size.Height))
}

func validateLocalPoint(el *axuiautomation.Element, x, y int) error {
	if el == nil {
		return fmt.Errorf("target disappeared")
	}
	if x < 0 || y < 0 {
		return fmt.Errorf("local coordinates must be non-negative")
	}
	w, h := localSize(el)
	if w > 0 && x >= w {
		return fmt.Errorf("x=%d outside target width %d", x, w)
	}
	if h > 0 && y >= h {
		return fmt.Errorf("y=%d outside target height %d", y, h)
	}
	return nil
}

func clickLocalPoint(el *axuiautomation.Element, x, y int) error {
	if err := validateLocalPoint(el, x, y); err != nil {
		return err
	}
	absX, absY := localPointToScreen(el, x, y)
	return mouseClickScreenPoint(absX, absY, cgEventLeftMouseDown, cgEventLeftMouseUp, cgMouseButtonLeft, el)
}

func hoverLocalPoint(el *axuiautomation.Element, x, y int) error {
	if err := validateLocalPoint(el, x, y); err != nil {
		return err
	}
	initCGMouseEvents()
	if cgWarpMouseCursorPosition == nil {
		return fmt.Errorf("CGWarpMouseCursorPosition not available")
	}
	absX, absY := localPointToScreen(el, x, y)
	ghostcursor.HoverAt(absX, absY)
	noteCLIVisualFeedback()
	cgWarpMouseCursorPosition(float64(absX), float64(absY))
	return nil
}

func localPointToScreen(el *axuiautomation.Element, x, y int) (int, int) {
	frame := el.Frame()
	absX := int(math.Round(frame.Origin.X)) + x
	absY := int(math.Round(frame.Origin.Y)) + y
	return absX, absY
}

func clickScreenPoint(x, y int) error {
	return mouseClickScreenPoint(x, y, cgEventLeftMouseDown, cgEventLeftMouseUp, cgMouseButtonLeft, nil)
}

func doubleClickLocalPoint(el *axuiautomation.Element, x, y int) error {
	if err := validateLocalPoint(el, x, y); err != nil {
		return err
	}
	absX, absY := localPointToScreen(el, x, y)
	return doubleClickScreenPoint(absX, absY, el)
}

func rightClickLocalPoint(el *axuiautomation.Element, x, y int) error {
	if err := validateLocalPoint(el, x, y); err != nil {
		return err
	}
	absX, absY := localPointToScreen(el, x, y)
	return mouseClickScreenPoint(absX, absY, cgEventRightMouseDown, cgEventRightMouseUp, cgMouseButtonRight, el)
}

func rightClickScreenPoint(x, y int) error {
	return mouseClickScreenPoint(x, y, cgEventRightMouseDown, cgEventRightMouseUp, cgMouseButtonRight, nil)
}

func dragLocalPoint(el *axuiautomation.Element, startX, startY, endX, endY int, button int32) error {
	if err := validateLocalPoint(el, startX, startY); err != nil {
		return err
	}
	if err := validateLocalPoint(el, endX, endY); err != nil {
		return err
	}
	absStartX, absStartY := localPointToScreen(el, startX, startY)
	absEndX, absEndY := localPointToScreen(el, endX, endY)
	return dragScreenPoint(absStartX, absStartY, absEndX, absEndY, button, 0, 0)
}

func doubleClickScreenPoint(x, y int, owner *axuiautomation.Element) error {
	initCGMouseEvents()
	switch {
	case cgEventCreateMouseEvent == nil:
		return fmt.Errorf("CGEventCreateMouseEvent not available")
	case cgEventPost == nil:
		return fmt.Errorf("CGEventPost not available")
	case cgEventSetIntegerValueField == nil:
		return fmt.Errorf("CGEventSetIntegerValueField not available")
	}

	glideCursorTo(x, y, owner)
	pauseForCursor(owner, cursorSettleDuration())
	flashClickActivity(x, y, owner)
	noteCLIVisualFeedback()
	if err := postMouseClickEvent(x, y, cgEventLeftMouseDown, cgEventLeftMouseUp, cgMouseButtonLeft, 1); err != nil {
		ghostcursor.Hide()
		return err
	}
	time.Sleep(50 * time.Millisecond)
	if err := postMouseClickEvent(x, y, cgEventLeftMouseDown, cgEventLeftMouseUp, cgMouseButtonLeft, 2); err != nil {
		ghostcursor.Hide()
		return err
	}
	settleClickActivity(x, y, owner)
	pauseForCursor(owner, cursorHoldDuration())
	return nil
}

func mouseClickScreenPoint(x, y int, downType, upType, button int32, owner *axuiautomation.Element) error {
	initCGMouseEvents()
	switch {
	case cgEventCreateMouseEvent == nil:
		return fmt.Errorf("CGEventCreateMouseEvent not available")
	case cgEventPost == nil:
		return fmt.Errorf("CGEventPost not available")
	}

	glideCursorTo(x, y, owner)
	pauseForCursor(owner, cursorSettleDuration())
	flashClickActivity(x, y, owner)
	noteCLIVisualFeedback()
	if err := postMouseClickEvent(x, y, downType, upType, button, 0); err != nil {
		ghostcursor.Hide()
		return err
	}
	settleClickActivity(x, y, owner)
	pauseForCursor(owner, cursorHoldDuration())
	return nil
}

// pauseForCursor sleeps for d only when the ghost overlay is actually being
// drawn for owner — there's no point pacing for invisible animations.
func pauseForCursor(owner *axuiautomation.Element, d time.Duration) {
	if d <= 0 || !ghostcursor.Enabled() || !targetVisible(owner) {
		return
	}
	time.Sleep(d)
}

func postMouseClickEvent(x, y int, downType, upType, button int32, clickState int64) error {
	mouseDown := cgEventCreateMouseEvent(0, downType, float64(x), float64(y), button)
	if mouseDown == 0 {
		return fmt.Errorf("failed to create mouse down event")
	}
	if clickState > 0 && cgEventSetIntegerValueField != nil {
		cgEventSetIntegerValueField(mouseDown, cgMouseEventClickState, clickState)
	}
	cgEventPost(cgHIDEventTap, mouseDown)
	corefoundation.CFRelease(corefoundation.CFTypeRef(mouseDown))

	time.Sleep(50 * time.Millisecond)

	mouseUp := cgEventCreateMouseEvent(0, upType, float64(x), float64(y), button)
	if mouseUp == 0 {
		return fmt.Errorf("failed to create mouse up event")
	}
	if clickState > 0 && cgEventSetIntegerValueField != nil {
		cgEventSetIntegerValueField(mouseUp, cgMouseEventClickState, clickState)
	}
	cgEventPost(cgHIDEventTap, mouseUp)
	corefoundation.CFRelease(corefoundation.CFTypeRef(mouseUp))
	return nil
}

func dragScreenPoint(startX, startY, endX, endY int, button int32, steps int, duration time.Duration) error {
	initCGMouseEvents()
	switch {
	case cgWarpMouseCursorPosition == nil:
		return fmt.Errorf("CGWarpMouseCursorPosition not available")
	case cgEventCreateMouseEvent == nil:
		return fmt.Errorf("CGEventCreateMouseEvent not available")
	case cgEventPost == nil:
		return fmt.Errorf("CGEventPost not available")
	}

	downType, draggedType, upType, err := dragEventTypes(button)
	if err != nil {
		return err
	}
	if steps <= 0 {
		distance := math.Hypot(float64(endX-startX), float64(endY-startY))
		steps = int(math.Ceil(distance / 24))
		if steps < 4 {
			steps = 4
		}
	}
	if duration <= 0 {
		duration = 250 * time.Millisecond
	}
	path, err := ghostcursor.SamplePath(
		ghostcursor.ScreenPosition(startX, startY),
		ghostcursor.ScreenPosition(endX, endY),
		ghostcursor.MoveOptions{
			Duration:   duration,
			Activity:   ghostcursor.ActivityDragging,
			CurveStyle: ghostcursor.CurveBezier,
		},
	)
	if err != nil {
		return fmt.Errorf("sample drag path: %w", err)
	}
	interval := 10 * time.Millisecond
	if len(path) > 1 {
		interval = duration / time.Duration(len(path)-1)
		if interval < 5*time.Millisecond {
			interval = 5 * time.Millisecond
		}
	}

	ghostcursor.PressAt(startX, startY)
	noteCLIVisualFeedback()
	cgWarpMouseCursorPosition(float64(startX), float64(startY))
	time.Sleep(10 * time.Millisecond)

	mouseDown := cgEventCreateMouseEvent(0, downType, float64(startX), float64(startY), button)
	if mouseDown == 0 {
		ghostcursor.Hide()
		return fmt.Errorf("failed to create mouse down event")
	}
	cgEventPost(cgHIDEventTap, mouseDown)
	corefoundation.CFRelease(corefoundation.CFTypeRef(mouseDown))

	for i := 1; i < len(path); i++ {
		x := int(math.Round(path[i].X))
		y := int(math.Round(path[i].Y))
		ghostcursor.DragTo(x, y)
		dragged := cgEventCreateMouseEvent(0, draggedType, float64(x), float64(y), button)
		if dragged == 0 {
			ghostcursor.Hide()
			return fmt.Errorf("failed to create mouse drag event")
		}
		cgEventPost(cgHIDEventTap, dragged)
		corefoundation.CFRelease(corefoundation.CFTypeRef(dragged))
		if i+1 < len(path) {
			time.Sleep(interval)
		}
	}

	mouseUp := cgEventCreateMouseEvent(0, upType, float64(endX), float64(endY), button)
	if mouseUp == 0 {
		ghostcursor.Hide()
		return fmt.Errorf("failed to create mouse up event")
	}
	cgEventPost(cgHIDEventTap, mouseUp)
	corefoundation.CFRelease(corefoundation.CFTypeRef(mouseUp))
	ghostcursor.ReleaseAt(endX, endY)
	return nil
}

func dragEventTypes(button int32) (downType, draggedType, upType int32, err error) {
	switch button {
	case cgMouseButtonLeft:
		return cgEventLeftMouseDown, cgEventLeftMouseDragged, cgEventLeftMouseUp, nil
	case cgMouseButtonRight:
		return cgEventRightMouseDown, cgEventRightMouseDragged, cgEventRightMouseUp, nil
	default:
		return 0, 0, 0, fmt.Errorf("unsupported drag button %d", button)
	}
}

func scrollLocalPoint(el *axuiautomation.Element, x, y int, direction axuiautomation.ScrollDirection, amount int) error {
	if err := validateLocalPoint(el, x, y); err != nil {
		return err
	}
	absX, absY := localPointToScreen(el, x, y)
	return scrollScreenPoint(absX, absY, direction, amount)
}

func scrollScreenPoint(x, y int, direction axuiautomation.ScrollDirection, amount int) error {
	initCGMouseEvents()
	switch {
	case cgWarpMouseCursorPosition == nil:
		return fmt.Errorf("CGWarpMouseCursorPosition not available")
	case cgEventCreateScrollWheelEvt == nil:
		return fmt.Errorf("CGEventCreateScrollWheelEvent not available")
	case cgEventPost == nil:
		return fmt.Errorf("CGEventPost not available")
	}
	if amount <= 0 {
		return nil
	}

	var wheel1, wheel2 int32
	switch direction {
	case axuiautomation.ScrollUp:
		wheel1 = int32(amount)
	case axuiautomation.ScrollDown:
		wheel1 = -int32(amount)
	case axuiautomation.ScrollLeft:
		wheel2 = int32(amount)
	case axuiautomation.ScrollRight:
		wheel2 = -int32(amount)
	default:
		return fmt.Errorf("unknown scroll direction %v", direction)
	}

	cgWarpMouseCursorPosition(float64(x), float64(y))
	time.Sleep(10 * time.Millisecond)

	evt := cgEventCreateScrollWheelEvt(0, 1, 2, wheel1, wheel2, 0)
	if evt == 0 {
		return fmt.Errorf("failed to create scroll wheel event")
	}
	cgEventPost(cgHIDEventTap, evt)
	corefoundation.CFRelease(corefoundation.CFTypeRef(evt))
	return nil
}

func preferredClickPoint(snapshot elementSnapshot) (int, int, bool) {
	record := snapshot.record
	if !isRowLikeRole(record.role) || record.w <= 0 || record.h <= 0 {
		return 0, 0, false
	}
	x := 12
	if record.w <= x {
		x = record.w / 2
	}
	y := record.h / 2
	if y >= record.h {
		y = record.h - 1
	}
	if x < 0 || y < 0 {
		return 0, 0, false
	}
	return x, y, true
}

func isRowLikeRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "AXCell", "AXOutlineRow", "AXRow":
		return true
	}
	return false
}

func centerClickPoint(snapshot elementSnapshot) (int, int, bool) {
	record := snapshot.record
	if record.w <= 0 || record.h <= 0 {
		return 0, 0, false
	}
	x := record.w / 2
	y := record.h / 2
	if x >= record.w {
		x = record.w - 1
	}
	if y >= record.h {
		y = record.h - 1
	}
	if x < 0 || y < 0 {
		return 0, 0, false
	}
	return x, y, true
}

func prefersAXPress(role string) bool {
	role = strings.TrimSpace(role)
	switch role {
	case "AXRadioButton":
		return false
	}
	switch role {
	case "AXButton",
		"AXCheckBox",
		"AXDisclosureTriangle",
		"AXLink",
		"AXMenuBarItem",
		"AXMenuButton",
		"AXMenuItem",
		"AXPopUpButton",
		"AXRadioButton",
		"AXSegment",
		"AXSwitch",
		"AXTab":
		return true
	}
	return strings.HasSuffix(role, "Button") || strings.HasSuffix(role, "Item")
}

func performAXPress(snapshot elementSnapshot) (string, error) {
	if snapshot.element == nil {
		return "", fmt.Errorf("target disappeared")
	}
	x, y := elementCenter(snapshot.element)
	glideCursorTo(x, y, snapshot.element)
	pauseForCursor(snapshot.element, cursorSettleDuration())
	flashClickActivity(x, y, snapshot.element)
	noteCLIVisualFeedback()
	if err := snapshot.element.PerformAction("AXPress"); err != nil {
		ghostcursor.Hide()
		return "", err
	}
	settleClickActivity(x, y, snapshot.element)
	pauseForCursor(snapshot.element, cursorHoldDuration())
	axuiautomation.SpinRunLoop(200 * time.Millisecond)
	return fmt.Sprintf("clicked %s via AXPress", formatSnapshot(snapshot)), nil
}

func performDefaultHover(snapshot elementSnapshot) (string, error) {
	if snapshot.element == nil {
		return "", fmt.Errorf("target disappeared")
	}
	if x, y, ok := preferredClickPoint(snapshot); ok {
		if err := hoverLocalPoint(snapshot.element, x, y); err == nil {
			return fmt.Sprintf("hovered %s at hit point %d,%d", formatSnapshot(snapshot), x, y), nil
		}
	}
	if x, y, ok := centerClickPoint(snapshot); ok {
		if err := hoverLocalPoint(snapshot.element, x, y); err == nil {
			return fmt.Sprintf("hovered %s at center %d,%d", formatSnapshot(snapshot), x, y), nil
		}
	}
	if err := snapshot.element.Hover(); err != nil {
		return "", err
	}
	return fmt.Sprintf("hovered %s", formatSnapshot(snapshot)), nil
}

func performAXShowMenu(snapshot elementSnapshot) (string, error) {
	if snapshot.element == nil {
		return "", fmt.Errorf("target disappeared")
	}
	x, y := elementCenter(snapshot.element)
	glideCursorTo(x, y, snapshot.element)
	pauseForCursor(snapshot.element, cursorSettleDuration())
	flashClickActivity(x, y, snapshot.element)
	noteCLIVisualFeedback()
	if err := snapshot.element.PerformAction("AXShowMenu"); err != nil {
		ghostcursor.Hide()
		return "", err
	}
	settleClickActivity(x, y, snapshot.element)
	pauseForCursor(snapshot.element, cursorHoldDuration())
	axuiautomation.SpinRunLoop(200 * time.Millisecond)
	return fmt.Sprintf("right-clicked %s via AXShowMenu", formatSnapshot(snapshot)), nil
}

func elementCenter(el *axuiautomation.Element) (int, int) {
	frame := el.Frame()
	return int(math.Round(frame.Origin.X + frame.Size.Width/2)),
		int(math.Round(frame.Origin.Y + frame.Size.Height/2))
}

func performDefaultRightClick(snapshot elementSnapshot) (string, error) {
	if snapshot.element == nil {
		return "", fmt.Errorf("target disappeared")
	}
	if !isRowLikeRole(snapshot.record.role) {
		if summary, err := performAXShowMenu(snapshot); err == nil {
			return summary, nil
		}
	}
	if x, y, ok := preferredClickPoint(snapshot); ok {
		if err := rightClickLocalPoint(snapshot.element, x, y); err == nil {
			return fmt.Sprintf("right-clicked %s at hit point %d,%d", formatSnapshot(snapshot), x, y), nil
		}
	}
	if x, y, ok := centerClickPoint(snapshot); ok {
		if err := rightClickLocalPoint(snapshot.element, x, y); err == nil {
			return fmt.Sprintf("right-clicked %s at center %d,%d", formatSnapshot(snapshot), x, y), nil
		}
	}
	return performAXShowMenu(snapshot)
}

func performDefaultClick(snapshot elementSnapshot) (string, error) {
	if snapshot.element == nil {
		return "", fmt.Errorf("target disappeared")
	}
	if prefersAXPress(snapshot.record.role) {
		if summary, err := performAXPress(snapshot); err == nil {
			return summary, nil
		}
	}

	if x, y, ok := preferredClickPoint(snapshot); ok {
		if err := clickLocalPoint(snapshot.element, x, y); err == nil {
			return fmt.Sprintf("clicked %s at hit point %d,%d", formatSnapshot(snapshot), x, y), nil
		}
	}
	if x, y, ok := centerClickPoint(snapshot); ok {
		if err := clickLocalPoint(snapshot.element, x, y); err == nil {
			return fmt.Sprintf("clicked %s at center %d,%d", formatSnapshot(snapshot), x, y), nil
		}
	}
	return performAXPress(snapshot)
}

func performDefaultDoubleClick(snapshot elementSnapshot) (string, error) {
	if snapshot.element == nil {
		return "", fmt.Errorf("target disappeared")
	}
	if x, y, ok := preferredClickPoint(snapshot); ok {
		if err := doubleClickLocalPoint(snapshot.element, x, y); err == nil {
			return fmt.Sprintf("double-clicked %s at hit point %d,%d", formatSnapshot(snapshot), x, y), nil
		}
	}
	if x, y, ok := centerClickPoint(snapshot); ok {
		if err := doubleClickLocalPoint(snapshot.element, x, y); err == nil {
			return fmt.Sprintf("double-clicked %s at center %d,%d", formatSnapshot(snapshot), x, y), nil
		}
	}
	return "", fmt.Errorf("double-click %s: no usable click point", formatSnapshot(snapshot))
}

func performDefaultScroll(snapshot elementSnapshot, direction axuiautomation.ScrollDirection, amount int) (string, error) {
	if snapshot.element == nil {
		return "", fmt.Errorf("target disappeared")
	}
	if amount <= 0 {
		return "", fmt.Errorf("scroll amount must be positive")
	}
	if x, y, ok := centerClickPoint(snapshot); ok {
		if err := scrollLocalPoint(snapshot.element, x, y, direction, amount); err == nil {
			return fmt.Sprintf("scrolled %s at center %d,%d by %d lines", formatSnapshot(snapshot), x, y, amount), nil
		}
	}
	return "", fmt.Errorf("scroll %s: no usable scroll point", formatSnapshot(snapshot))
}

func focusElement(el *axuiautomation.Element) error {
	if el == nil {
		return fmt.Errorf("target disappeared")
	}
	if err := el.Focus(); err == nil {
		return nil
	}
	_, err := performDefaultClick(snapshotElement(el, 0, 0))
	return err
}
