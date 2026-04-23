package ui

import (
	"strings"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/foundation"
)

const (
	aeCoreSuiteEventClass = uint32('a')<<24 | uint32('e')<<16 | uint32('v')<<8 | uint32('t')
	aeQuitApplicationID   = uint32('q')<<24 | uint32('u')<<16 | uint32('i')<<8 | uint32('t')
)

var currentAppleEventInfo = func() (eventClass, eventID uint32, ok bool) {
	event := foundation.GetNSAppleEventManagerClass().SharedAppleEventManager().CurrentAppleEvent()
	if event.GetID() == 0 {
		return 0, 0, false
	}
	return event.EventClass(), event.EventID(), true
}

var currentKeyEventInfo = func(app appkit.NSApplication) (eventType appkit.NSEventType, modifiers appkit.NSEventModifierFlags, chars string, ok bool) {
	event := app.CurrentEvent()
	if event.GetID() == 0 {
		return 0, 0, "", false
	}
	return event.Type(), event.ModifierFlags(), event.CharactersIgnoringModifiers(), true
}

func ShouldTerminateReply(app appkit.NSApplication) appkit.NSApplicationTerminateReply {
	if terminationRequestedByUser(app) {
		return appkit.NSTerminateNow
	}
	if ScreenCaptureTerminateGuardActive() {
		return appkit.NSTerminateCancel
	}
	return appkit.NSTerminateCancel
}

func terminationRequestedByUser(app appkit.NSApplication) bool {
	if eventClass, eventID, ok := currentAppleEventInfo(); ok {
		return eventClass == aeCoreSuiteEventClass && eventID == aeQuitApplicationID
	}
	eventType, modifiers, chars, ok := currentKeyEventInfo(app)
	if !ok || eventType != appkit.NSEventTypeKeyDown {
		return false
	}
	if modifiers&appkit.NSEventModifierFlagCommand == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(chars), "q")
}
