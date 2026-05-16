package input

import (
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/x/axuiautomation"
)

const focusAttrEncodingUTF8 = 0x08000100

type focusState struct {
	window               *axuiautomation.Element
	releaseWindow        bool
	element              *axuiautomation.Element
	priorWindowFocused   *bool
	priorWindowMain      *bool
	priorElementFocused  *bool
	suppressionAttempted bool
}

func withSyntheticFocus(el *axuiautomation.Element, fn func() error) error {
	state := suppressFocus(el)
	err := fn()
	restoreFocus(state)
	return err
}

func suppressFocus(el *axuiautomation.Element) focusState {
	state := focusState{element: el}
	if el == nil {
		return state
	}
	window, release := enclosingWindow(el)
	state.window = window
	state.releaseWindow = release

	if minimized := readAXBool(window, "AXMinimized"); minimized != nil && *minimized {
		return state
	}

	state.priorWindowFocused = readAXBool(window, "AXFocused")
	state.priorWindowMain = readAXBool(window, "AXMain")
	state.priorElementFocused = readAXBool(el, "AXFocused")
	writeAXBool(window, "AXFocused", true)
	writeAXBool(window, "AXMain", true)
	writeAXBool(el, "AXFocused", true)
	state.suppressionAttempted = true
	return state
}

func restoreFocus(state focusState) {
	if state.suppressionAttempted {
		if state.priorWindowFocused != nil {
			writeAXBool(state.window, "AXFocused", *state.priorWindowFocused)
		}
		if state.priorWindowMain != nil {
			writeAXBool(state.window, "AXMain", *state.priorWindowMain)
		}
		if state.priorElementFocused != nil {
			writeAXBool(state.element, "AXFocused", *state.priorElementFocused)
		}
	}
	if state.releaseWindow && state.window != nil {
		state.window.Release()
	}
}

func enclosingWindow(el *axuiautomation.Element) (*axuiautomation.Element, bool) {
	if el == nil {
		return nil, false
	}
	if el.Role() == "AXWindow" {
		return el, false
	}
	cur := el.Parent()
	for cur != nil {
		if cur.Role() == "AXWindow" {
			return cur, true
		}
		next := cur.Parent()
		cur.Release()
		cur = next
	}
	return nil, false
}

func readAXBool(el *axuiautomation.Element, name string) *bool {
	if el == nil || el.Ref() == 0 {
		return nil
	}
	attr := cfString(name)
	if attr == 0 {
		return nil
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(attr))
	var value uintptr
	err := axuiautomation.AXUIElementCopyAttributeValue(el.Ref(), uintptr(attr), &value)
	if int(err) != 0 || value == 0 {
		return nil
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(value))
	if corefoundation.CFGetTypeID(corefoundation.CFTypeRef(value)) != corefoundation.CFBooleanGetTypeID() {
		return nil
	}
	v := corefoundation.CFBooleanGetValue(corefoundation.CFBooleanRef(value))
	return &v
}

func writeAXBool(el *axuiautomation.Element, name string, value bool) bool {
	if el == nil || el.Ref() == 0 {
		return false
	}
	attr := cfString(name)
	if attr == 0 {
		return false
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(attr))
	cfValue := corefoundation.KCFBooleanFalse
	if value {
		cfValue = corefoundation.KCFBooleanTrue
	}
	err := axuiautomation.AXUIElementSetAttributeValue(el.Ref(), uintptr(attr), uintptr(cfValue))
	return int(err) == 0
}

func cfString(s string) corefoundation.CFStringRef {
	return corefoundation.CFStringCreateWithCString(0, s, focusAttrEncodingUTF8)
}
