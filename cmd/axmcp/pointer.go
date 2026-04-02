package main

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/x/axuiautomation"
)

var (
	cgEventCreateMouseEvent     func(source uintptr, mouseType int32, x, y float64, button int32) uintptr
	cgEventCreateScrollWheelEvt func(source uintptr, units int32, wheelCount uint32, wheel1, wheel2, wheel3 int32) uintptr
	cgEventPost                 func(tap int32, event uintptr)
	cgEventSetIntegerValueField func(event uintptr, field uint32, value int64)
	cgWarpMouseCursorPosition   func(x, y float64) int32
	cgMouseEventsOnce           sync.Once
)

const (
	cgEventLeftMouseDown   = 1
	cgEventLeftMouseUp     = 2
	cgEventRightMouseDown  = 3
	cgEventRightMouseUp    = 4
	cgMouseEventClickState = 1
	cgMouseButtonLeft      = 0
	cgMouseButtonRight     = 1
	cgHIDEventTap          = 0
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
	return clickScreenPoint(absX, absY)
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
	return mouseClickScreenPoint(x, y, cgEventLeftMouseDown, cgEventLeftMouseUp, cgMouseButtonLeft)
}

func doubleClickLocalPoint(el *axuiautomation.Element, x, y int) error {
	if err := validateLocalPoint(el, x, y); err != nil {
		return err
	}
	absX, absY := localPointToScreen(el, x, y)
	return doubleClickScreenPoint(absX, absY)
}

func rightClickLocalPoint(el *axuiautomation.Element, x, y int) error {
	if err := validateLocalPoint(el, x, y); err != nil {
		return err
	}
	absX, absY := localPointToScreen(el, x, y)
	return rightClickScreenPoint(absX, absY)
}

func rightClickScreenPoint(x, y int) error {
	return mouseClickScreenPoint(x, y, cgEventRightMouseDown, cgEventRightMouseUp, cgMouseButtonRight)
}

func doubleClickScreenPoint(x, y int) error {
	initCGMouseEvents()
	switch {
	case cgWarpMouseCursorPosition == nil:
		return fmt.Errorf("CGWarpMouseCursorPosition not available")
	case cgEventCreateMouseEvent == nil:
		return fmt.Errorf("CGEventCreateMouseEvent not available")
	case cgEventPost == nil:
		return fmt.Errorf("CGEventPost not available")
	case cgEventSetIntegerValueField == nil:
		return fmt.Errorf("CGEventSetIntegerValueField not available")
	}

	cgWarpMouseCursorPosition(float64(x), float64(y))
	time.Sleep(10 * time.Millisecond)

	if err := postMouseClickEvent(x, y, cgEventLeftMouseDown, cgEventLeftMouseUp, cgMouseButtonLeft, 1); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return postMouseClickEvent(x, y, cgEventLeftMouseDown, cgEventLeftMouseUp, cgMouseButtonLeft, 2)
}

func mouseClickScreenPoint(x, y int, downType, upType, button int32) error {
	initCGMouseEvents()
	switch {
	case cgWarpMouseCursorPosition == nil:
		return fmt.Errorf("CGWarpMouseCursorPosition not available")
	case cgEventCreateMouseEvent == nil:
		return fmt.Errorf("CGEventCreateMouseEvent not available")
	case cgEventPost == nil:
		return fmt.Errorf("CGEventPost not available")
	}

	cgWarpMouseCursorPosition(float64(x), float64(y))
	time.Sleep(10 * time.Millisecond)
	return postMouseClickEvent(x, y, downType, upType, button, 0)
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
	if err := snapshot.element.PerformAction("AXPress"); err != nil {
		return "", err
	}
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
	if err := snapshot.element.PerformAction("AXShowMenu"); err != nil {
		return "", err
	}
	axuiautomation.SpinRunLoop(200 * time.Millisecond)
	return fmt.Sprintf("right-clicked %s via AXShowMenu", formatSnapshot(snapshot)), nil
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
