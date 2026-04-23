package ui

import (
	"testing"

	"github.com/tmc/apple/appkit"
)

func TestShouldTerminateReplyAllowsQuitAppleEvent(t *testing.T) {
	oldApple := currentAppleEventInfo
	oldKey := currentKeyEventInfo
	defer func() {
		currentAppleEventInfo = oldApple
		currentKeyEventInfo = oldKey
	}()

	currentAppleEventInfo = func() (uint32, uint32, bool) {
		return aeCoreSuiteEventClass, aeQuitApplicationID, true
	}
	currentKeyEventInfo = func(appkit.NSApplication) (appkit.NSEventType, appkit.NSEventModifierFlags, string, bool) {
		return 0, 0, "", false
	}

	if got := ShouldTerminateReply(appkit.NSApplication{}); got != appkit.NSTerminateNow {
		t.Fatalf("ShouldTerminateReply() = %v, want %v", got, appkit.NSTerminateNow)
	}
}

func TestShouldTerminateReplyAllowsCommandQ(t *testing.T) {
	oldApple := currentAppleEventInfo
	oldKey := currentKeyEventInfo
	defer func() {
		currentAppleEventInfo = oldApple
		currentKeyEventInfo = oldKey
	}()

	currentAppleEventInfo = func() (uint32, uint32, bool) {
		return 0, 0, false
	}
	currentKeyEventInfo = func(appkit.NSApplication) (appkit.NSEventType, appkit.NSEventModifierFlags, string, bool) {
		return appkit.NSEventTypeKeyDown, appkit.NSEventModifierFlagCommand, "q", true
	}

	if got := ShouldTerminateReply(appkit.NSApplication{}); got != appkit.NSTerminateNow {
		t.Fatalf("ShouldTerminateReply() = %v, want %v", got, appkit.NSTerminateNow)
	}
}

func TestShouldTerminateReplyCancelsBackgroundTerminate(t *testing.T) {
	oldApple := currentAppleEventInfo
	oldKey := currentKeyEventInfo
	origScreen := permissionInProgress("ScreenCapture")
	defer func() {
		currentAppleEventInfo = oldApple
		currentKeyEventInfo = oldKey
		setPermissionInProgress("ScreenCapture", origScreen)
	}()

	currentAppleEventInfo = func() (uint32, uint32, bool) {
		return 0, 0, false
	}
	currentKeyEventInfo = func(appkit.NSApplication) (appkit.NSEventType, appkit.NSEventModifierFlags, string, bool) {
		return 0, 0, "", false
	}

	if got := ShouldTerminateReply(appkit.NSApplication{}); got != appkit.NSTerminateCancel {
		t.Fatalf("ShouldTerminateReply() = %v, want %v", got, appkit.NSTerminateCancel)
	}
}

func TestShouldTerminateReplyAllowsUserQuitDuringScreenCaptureFlow(t *testing.T) {
	oldApple := currentAppleEventInfo
	oldKey := currentKeyEventInfo
	origScreen := permissionInProgress("ScreenCapture")
	defer func() {
		currentAppleEventInfo = oldApple
		currentKeyEventInfo = oldKey
		setPermissionInProgress("ScreenCapture", origScreen)
	}()

	setPermissionInProgress("ScreenCapture", true)
	currentAppleEventInfo = func() (uint32, uint32, bool) {
		return aeCoreSuiteEventClass, aeQuitApplicationID, true
	}
	currentKeyEventInfo = func(appkit.NSApplication) (appkit.NSEventType, appkit.NSEventModifierFlags, string, bool) {
		return 0, 0, "", false
	}

	if got := ShouldTerminateReply(appkit.NSApplication{}); got != appkit.NSTerminateNow {
		t.Fatalf("ShouldTerminateReply() = %v, want %v", got, appkit.NSTerminateNow)
	}
}
