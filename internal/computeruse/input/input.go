package input

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/coords"
	"github.com/tmc/axmcp/internal/ghostcursor"
)

type LocalPoint struct {
	X int
	Y int
}

type KeyCombo struct {
	KeyCode uint16
	Shift   bool
	Control bool
	Option  bool
	Command bool
	Label   string
}

var (
	cgEventCreateMouseEvent   func(source uintptr, mouseType int32, x, y float64, button int32) uintptr
	cgEventPost               func(tap int32, event uintptr)
	cgWarpMouseCursorPosition func(x, y float64) int32
	cgMouseEventsOnce         sync.Once
)

const (
	cgEventLeftMouseDown     = 1
	cgEventLeftMouseUp       = 2
	cgEventRightMouseDown    = 3
	cgEventRightMouseUp      = 4
	cgEventLeftMouseDragged  = 6
	cgEventRightMouseDragged = 7
	cgEventOtherMouseDown    = 25
	cgEventOtherMouseUp      = 26
	cgEventOtherMouseDragged = 27
	cgMouseButtonLeft        = 0
	cgMouseButtonRight       = 1
	cgMouseButtonMiddle      = 2
	cgHIDEventTap            = 0
)

var knownKeys = map[string]uint16{
	"return": 0x24, "enter": 0x24, "tab": 0x30, "escape": 0x35, "esc": 0x35,
	"delete": 0x33, "backspace": 0x33, "space": 0x31,
	"up": 0x7E, "down": 0x7D, "left": 0x7B, "right": 0x7C,
	"home": 0x73, "end": 0x77, "pageup": 0x74, "pagedown": 0x79,
	"-": 0x1B, "=": 0x18,
	"0": 0x1D, "1": 0x12, "2": 0x13, "3": 0x14, "4": 0x15,
	"5": 0x17, "6": 0x16, "7": 0x1A, "8": 0x1C, "9": 0x19,
	"a": 0x00, "b": 0x0B, "c": 0x08, "d": 0x02, "e": 0x0E,
	"f": 0x03, "g": 0x05, "h": 0x04, "i": 0x22, "j": 0x26,
	"k": 0x28, "l": 0x25, "m": 0x2E, "n": 0x2D, "o": 0x1F,
	"p": 0x23, "q": 0x0C, "r": 0x0F, "s": 0x01, "t": 0x11,
	"u": 0x20, "v": 0x09, "w": 0x0D, "x": 0x07, "y": 0x10, "z": 0x06,
}

func initCGMouseEvents() {
	cgMouseEventsOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&cgEventCreateMouseEvent, lib, "CGEventCreateMouseEvent")
		purego.RegisterLibFunc(&cgEventPost, lib, "CGEventPost")
		purego.RegisterLibFunc(&cgWarpMouseCursorPosition, lib, "CGWarpMouseCursorPosition")
	})
}

func ScreenshotPointToWindowLocal(window computeruse.WindowInfo, x, y int) (LocalPoint, error) {
	point, err := coords.ScreenshotPointToWindowLocal(window, x, y)
	return LocalPoint{X: point.X, Y: point.Y}, err
}

func ClickElement(el *axuiautomation.Element, button string, clickCount int) error {
	if el == nil {
		return fmt.Errorf("target disappeared")
	}
	center := elementCenter(el)
	switch strings.ToLower(strings.TrimSpace(button)) {
	case "", "left":
		if clickCount <= 1 {
			ghostcursor.PressAt(center.X, center.Y)
			if err := el.Click(); err != nil {
				ghostcursor.Hide()
				return err
			}
			ghostcursor.ReleaseAt(center.X, center.Y)
			return nil
		}
		if clickCount == 2 {
			ghostcursor.PressAt(center.X, center.Y)
			if err := el.DoubleClick(); err != nil {
				ghostcursor.Hide()
				return err
			}
			ghostcursor.ReleaseAt(center.X, center.Y)
			return nil
		}
		return fmt.Errorf("unsupported click_count %d", clickCount)
	case "right":
		ghostcursor.PressAt(center.X, center.Y)
		if err := el.PerformAction("AXShowMenu"); err != nil {
			ghostcursor.Hide()
			return err
		}
		ghostcursor.ReleaseAt(center.X, center.Y)
		return nil
	case "middle":
		return clickScreenPoint(elementCenter(el), cgEventOtherMouseDown, cgEventOtherMouseUp, cgMouseButtonMiddle, clickCount)
	default:
		return fmt.Errorf("invalid button %q; use left, right, or middle", button)
	}
}

func ClickElementAt(el *axuiautomation.Element, point LocalPoint, button string, clickCount int) error {
	if el == nil {
		return fmt.Errorf("target disappeared")
	}
	switch strings.ToLower(strings.TrimSpace(button)) {
	case "", "left":
		if clickCount <= 1 {
			return el.ClickAt(point.X, point.Y)
		}
		return clickScreenPoint(localPointToScreen(el, point), cgEventLeftMouseDown, cgEventLeftMouseUp, cgMouseButtonLeft, clickCount)
	case "right":
		return clickScreenPoint(localPointToScreen(el, point), cgEventRightMouseDown, cgEventRightMouseUp, cgMouseButtonRight, 1)
	case "middle":
		return clickScreenPoint(localPointToScreen(el, point), cgEventOtherMouseDown, cgEventOtherMouseUp, cgMouseButtonMiddle, clickCount)
	default:
		return fmt.Errorf("invalid button %q; use left, right, or middle", button)
	}
}

func DragElement(el *axuiautomation.Element, start, end LocalPoint, button string) error {
	if el == nil {
		return fmt.Errorf("target disappeared")
	}
	btn, err := parseButton(button)
	if err != nil {
		return err
	}
	startScreen := localPointToScreen(el, start)
	endScreen := localPointToScreen(el, end)
	return dragScreenPoint(startScreen, endScreen, btn)
}

func ScrollElement(el *axuiautomation.Element, direction string, pages float64) error {
	if el == nil {
		return fmt.Errorf("target disappeared")
	}
	dir, err := parseDirection(direction)
	if err != nil {
		return err
	}
	if pages == 0 {
		pages = 1
	}
	lines := int(math.Round(pages * 12))
	if lines == 0 {
		if pages > 0 {
			lines = 1
		} else {
			lines = -1
		}
	}
	if lines < 0 {
		lines = -lines
		dir = oppositeDirection(dir)
	}
	return el.Scroll(dir, lines)
}

func ParseKeyCombo(spec string) (KeyCombo, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return KeyCombo{}, fmt.Errorf("keys are required")
	}
	parts := strings.Split(spec, "+")
	var combo KeyCombo
	for i, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if i < len(parts)-1 {
			switch part {
			case "cmd", "command", "super", "meta":
				combo.Command = true
				continue
			case "ctrl", "control":
				combo.Control = true
				continue
			case "alt", "option":
				combo.Option = true
				continue
			case "shift":
				combo.Shift = true
				continue
			}
		}
		keyCode, ok := knownKeys[part]
		if !ok {
			return KeyCombo{}, fmt.Errorf("unsupported key %q", part)
		}
		combo.KeyCode = keyCode
		combo.Label = part
	}
	if combo.Label == "" {
		return KeyCombo{}, fmt.Errorf("missing key in %q", spec)
	}
	return combo, nil
}

func SendKeyCombo(spec string) error {
	combo, err := ParseKeyCombo(spec)
	if err != nil {
		return err
	}
	return axuiautomation.SendKeyCombo(combo.KeyCode, combo.Shift, combo.Control, combo.Option, combo.Command)
}

func SendKeyComboToPID(pid int32, spec string) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	combo, err := ParseKeyCombo(spec)
	if err != nil {
		return err
	}
	flags := keyEventFlags(combo)
	keyDown := coregraphics.CGEventCreateKeyboardEvent(0, combo.KeyCode, true)
	if keyDown == 0 {
		return fmt.Errorf("failed to create key down event")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(keyDown))
	coregraphics.CGEventSetFlags(keyDown, flags)
	coregraphics.CGEventPostToPid(pid, keyDown)

	time.Sleep(10 * time.Millisecond)

	keyUp := coregraphics.CGEventCreateKeyboardEvent(0, combo.KeyCode, false)
	if keyUp == 0 {
		return fmt.Errorf("failed to create key up event")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(keyUp))
	coregraphics.CGEventSetFlags(keyUp, flags)
	coregraphics.CGEventPostToPid(pid, keyUp)
	return nil
}

func keyEventFlags(combo KeyCombo) coregraphics.CGEventFlags {
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
	return flags
}

func localPointToScreen(el *axuiautomation.Element, point LocalPoint) LocalPoint {
	frame := el.Frame()
	return LocalPoint{
		X: int(math.Round(frame.Origin.X)) + point.X,
		Y: int(math.Round(frame.Origin.Y)) + point.Y,
	}
}

func elementCenter(el *axuiautomation.Element) LocalPoint {
	frame := el.Frame()
	return LocalPoint{
		X: int(math.Round(frame.Origin.X + frame.Size.Width/2)),
		Y: int(math.Round(frame.Origin.Y + frame.Size.Height/2)),
	}
}

func clickScreenPoint(point LocalPoint, downType, upType, button int32, clickCount int) error {
	initCGMouseEvents()
	switch {
	case cgWarpMouseCursorPosition == nil:
		return fmt.Errorf("CGWarpMouseCursorPosition not available")
	case cgEventCreateMouseEvent == nil:
		return fmt.Errorf("CGEventCreateMouseEvent not available")
	case cgEventPost == nil:
		return fmt.Errorf("CGEventPost not available")
	}
	ghostcursor.PressAt(point.X, point.Y)
	cgWarpMouseCursorPosition(float64(point.X), float64(point.Y))
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < clickCount; i++ {
		mouseDown := cgEventCreateMouseEvent(0, downType, float64(point.X), float64(point.Y), button)
		if mouseDown == 0 {
			ghostcursor.Hide()
			return fmt.Errorf("failed to create mouse down event")
		}
		cgEventPost(cgHIDEventTap, mouseDown)
		corefoundation.CFRelease(corefoundation.CFTypeRef(mouseDown))
		time.Sleep(40 * time.Millisecond)
		mouseUp := cgEventCreateMouseEvent(0, upType, float64(point.X), float64(point.Y), button)
		if mouseUp == 0 {
			ghostcursor.Hide()
			return fmt.Errorf("failed to create mouse up event")
		}
		cgEventPost(cgHIDEventTap, mouseUp)
		corefoundation.CFRelease(corefoundation.CFTypeRef(mouseUp))
		if i < clickCount-1 {
			time.Sleep(40 * time.Millisecond)
		}
	}
	ghostcursor.ReleaseAt(point.X, point.Y)
	return nil
}

func dragScreenPoint(start, end LocalPoint, button int32) error {
	initCGMouseEvents()
	switch {
	case cgWarpMouseCursorPosition == nil:
		return fmt.Errorf("CGWarpMouseCursorPosition not available")
	case cgEventCreateMouseEvent == nil:
		return fmt.Errorf("CGEventCreateMouseEvent not available")
	case cgEventPost == nil:
		return fmt.Errorf("CGEventPost not available")
	}
	downType, dragType, upType, err := dragEventTypes(button)
	if err != nil {
		return err
	}
	distance := math.Hypot(float64(end.X-start.X), float64(end.Y-start.Y))
	steps := int(math.Ceil(distance / 24))
	if steps < 4 {
		steps = 4
	}
	duration := time.Duration(steps*10) * time.Millisecond
	path, err := ghostcursor.SamplePath(
		ghostcursor.ScreenPosition(start.X, start.Y),
		ghostcursor.ScreenPosition(end.X, end.Y),
		ghostcursor.MoveOptions{
			Duration:   duration,
			Activity:   ghostcursor.ActivityDragging,
			CurveStyle: ghostcursor.CurveBezier,
		},
	)
	if err != nil {
		return fmt.Errorf("sample drag path: %w", err)
	}
	stepSleep := 10 * time.Millisecond
	if len(path) > 1 {
		stepSleep = duration / time.Duration(len(path)-1)
		if stepSleep <= 0 {
			stepSleep = 10 * time.Millisecond
		}
	}
	ghostcursor.PressAt(start.X, start.Y)
	cgWarpMouseCursorPosition(float64(start.X), float64(start.Y))
	time.Sleep(10 * time.Millisecond)
	mouseDown := cgEventCreateMouseEvent(0, downType, float64(start.X), float64(start.Y), button)
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
		dragged := cgEventCreateMouseEvent(0, dragType, float64(x), float64(y), button)
		if dragged == 0 {
			ghostcursor.Hide()
			return fmt.Errorf("failed to create mouse drag event")
		}
		cgEventPost(cgHIDEventTap, dragged)
		corefoundation.CFRelease(corefoundation.CFTypeRef(dragged))
		if i+1 < len(path) {
			time.Sleep(stepSleep)
		}
	}
	mouseUp := cgEventCreateMouseEvent(0, upType, float64(end.X), float64(end.Y), button)
	if mouseUp == 0 {
		ghostcursor.Hide()
		return fmt.Errorf("failed to create mouse up event")
	}
	cgEventPost(cgHIDEventTap, mouseUp)
	corefoundation.CFRelease(corefoundation.CFTypeRef(mouseUp))
	ghostcursor.ReleaseAt(end.X, end.Y)
	return nil
}

func dragEventTypes(button int32) (downType, draggedType, upType int32, err error) {
	switch button {
	case cgMouseButtonLeft:
		return cgEventLeftMouseDown, cgEventLeftMouseDragged, cgEventLeftMouseUp, nil
	case cgMouseButtonRight:
		return cgEventRightMouseDown, cgEventRightMouseDragged, cgEventRightMouseUp, nil
	case cgMouseButtonMiddle:
		return cgEventOtherMouseDown, cgEventOtherMouseDragged, cgEventOtherMouseUp, nil
	default:
		return 0, 0, 0, fmt.Errorf("unsupported drag button %d", button)
	}
}

func parseButton(button string) (int32, error) {
	switch strings.ToLower(strings.TrimSpace(button)) {
	case "", "left":
		return cgMouseButtonLeft, nil
	case "right":
		return cgMouseButtonRight, nil
	case "middle":
		return cgMouseButtonMiddle, nil
	default:
		return 0, fmt.Errorf("invalid button %q; use left, right, or middle", button)
	}
}

func parseDirection(direction string) (axuiautomation.ScrollDirection, error) {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "up":
		return axuiautomation.ScrollUp, nil
	case "down":
		return axuiautomation.ScrollDown, nil
	case "left":
		return axuiautomation.ScrollLeft, nil
	case "right":
		return axuiautomation.ScrollRight, nil
	default:
		return 0, fmt.Errorf("invalid direction %q; use up, down, left, or right", direction)
	}
}

func oppositeDirection(direction axuiautomation.ScrollDirection) axuiautomation.ScrollDirection {
	switch direction {
	case axuiautomation.ScrollUp:
		return axuiautomation.ScrollDown
	case axuiautomation.ScrollDown:
		return axuiautomation.ScrollUp
	case axuiautomation.ScrollLeft:
		return axuiautomation.ScrollRight
	case axuiautomation.ScrollRight:
		return axuiautomation.ScrollLeft
	default:
		return direction
	}
}
