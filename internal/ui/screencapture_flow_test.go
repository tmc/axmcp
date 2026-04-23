package ui

import (
	"testing"
	"time"
)

func TestScreenCaptureWindowStateForInitialRequest(t *testing.T) {
	state := screenCaptureWindowStateFor(false, false, false, 0)
	if state.phase != screenCaptureWindowPhaseRequest {
		t.Fatalf("phase = %v, want request", state.phase)
	}
	if got := state.requestTitle; got != "Request Permission" {
		t.Fatalf("requestTitle = %q, want Request Permission", got)
	}
	if state.showSpinner {
		t.Fatal("showSpinner = true, want false before request")
	}
	if state.showWait {
		t.Fatal("showWait = true, want false before request")
	}
	if !state.requestEnabled {
		t.Fatal("requestEnabled = false, want true")
	}
}

func TestScreenCaptureWindowStateForPromptGrace(t *testing.T) {
	state := screenCaptureWindowStateFor(true, false, false, 2*time.Second)
	if state.phase != screenCaptureWindowPhasePrompt {
		t.Fatalf("phase = %v, want prompt", state.phase)
	}
	if got := state.requestTitle; got != "Waiting…" {
		t.Fatalf("requestTitle = %q, want Waiting…", got)
	}
	if !state.showSpinner || !state.showWait {
		t.Fatal("expected spinner and wait label during prompt grace")
	}
	if state.requestEnabled {
		t.Fatal("requestEnabled = true, want false while waiting for prompt")
	}
}

func TestScreenCaptureWindowStateForVisiblePrompt(t *testing.T) {
	state := screenCaptureWindowStateFor(true, true, false, 7*time.Second)
	if state.phase != screenCaptureWindowPhasePrompt {
		t.Fatalf("phase = %v, want prompt", state.phase)
	}
	if got := state.bodyText; got != "Approve the macOS prompt, then return here." {
		t.Fatalf("bodyText = %q", got)
	}
	if got := state.waitText; got != "Waiting for your response… (7s)" {
		t.Fatalf("waitText = %q", got)
	}
}

func TestScreenCaptureWindowStateForSettingsGuidance(t *testing.T) {
	state := screenCaptureWindowStateFor(true, false, false, 6*time.Second)
	if state.phase != screenCaptureWindowPhaseSettings {
		t.Fatalf("phase = %v, want settings", state.phase)
	}
	if got := state.requestTitle; got != "Open Settings" {
		t.Fatalf("requestTitle = %q, want Open Settings", got)
	}
	if got := state.bodyText; got != "If no prompt appeared, enable axmcp.app in System Settings." {
		t.Fatalf("bodyText = %q", got)
	}
	if !state.requestEnabled {
		t.Fatal("requestEnabled = false, want true in settings guidance phase")
	}
	if state.showReset {
		t.Fatal("showReset = true, want false before reset delay")
	}
}

func TestScreenCaptureWindowStateShowsResetAfterDelay(t *testing.T) {
	state := screenCaptureWindowStateFor(true, false, true, 12*time.Second)
	if state.phase != screenCaptureWindowPhaseSettings {
		t.Fatalf("phase = %v, want settings", state.phase)
	}
	if !state.showReset {
		t.Fatal("showReset = false, want true after prolonged wait")
	}
}

func TestIsScreenCapturePromptWindow(t *testing.T) {
	if !isScreenCapturePromptWindow(permissionWindowRecord{
		Owner: "universalAccessAuthWarn",
		Title: "Screen Recording",
	}) {
		t.Fatal("isScreenCapturePromptWindow = false, want true")
	}
	if isScreenCapturePromptWindow(permissionWindowRecord{
		Owner: "System Settings",
		Title: "Screen & System Audio Recording",
	}) {
		t.Fatal("isScreenCapturePromptWindow = true for settings window, want false")
	}
}

func TestIsScreenCaptureSettingsWindow(t *testing.T) {
	if !isScreenCaptureSettingsWindow(permissionWindowRecord{
		Owner: "System Settings",
		Title: "Screen & System Audio Recording",
	}) {
		t.Fatal("isScreenCaptureSettingsWindow = false, want true")
	}
	if !isScreenCaptureSettingsWindow(permissionWindowRecord{
		Owner: "System Settings (AppleScript)",
		Title: "Screen Recording",
	}) {
		t.Fatal("isScreenCaptureSettingsWindow = false for AppleScript mirror, want true")
	}
	if isScreenCaptureSettingsWindow(permissionWindowRecord{
		Owner: "System Settings",
		Title: "Accessibility",
	}) {
		t.Fatal("isScreenCaptureSettingsWindow = true for accessibility pane, want false")
	}
}
