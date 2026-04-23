package ui

import (
	"strings"
	"testing"
)

func TestCurrentPermissionSnapshotMissing(t *testing.T) {
	oldTrusted := axIsProcessTrusted
	oldTrustedWithOptions := axIsProcessTrustedWithOptions
	oldScreen := screenRecordingAvailable
	origAccess := permissionInProgress("Accessibility")
	origScreen := permissionInProgress("ScreenCapture")
	defer func() {
		axIsProcessTrusted = oldTrusted
		axIsProcessTrustedWithOptions = oldTrustedWithOptions
		screenRecordingAvailable = oldScreen
		setPermissionInProgress("Accessibility", origAccess)
		setPermissionInProgress("ScreenCapture", origScreen)
	}()

	axIsProcessTrusted = func() bool { return false }
	axIsProcessTrustedWithOptions = nil
	screenRecordingAvailable = func() bool { return false }
	setPermissionInProgress("Accessibility", false)
	setPermissionInProgress("ScreenCapture", false)

	snapshot := CurrentPermissionSnapshot()
	if got := snapshot.Accessibility; got != PermissionStatusMissing {
		t.Fatalf("Accessibility = %q, want %q", got, PermissionStatusMissing)
	}
	if got := snapshot.ScreenRecording; got != PermissionStatusMissing {
		t.Fatalf("ScreenRecording = %q, want %q", got, PermissionStatusMissing)
	}
	if !snapshot.Pending {
		t.Fatal("Pending = false, want true")
	}
	if snapshot.Message == "" {
		t.Fatal("Message = empty, want explanatory text")
	}
}

func TestCurrentPermissionSnapshotInProgress(t *testing.T) {
	oldTrusted := axIsProcessTrusted
	oldTrustedWithOptions := axIsProcessTrustedWithOptions
	oldScreen := screenRecordingAvailable
	origAccess := permissionInProgress("Accessibility")
	origScreen := permissionInProgress("ScreenCapture")
	defer func() {
		axIsProcessTrusted = oldTrusted
		axIsProcessTrustedWithOptions = oldTrustedWithOptions
		screenRecordingAvailable = oldScreen
		setPermissionInProgress("Accessibility", origAccess)
		setPermissionInProgress("ScreenCapture", origScreen)
	}()

	axIsProcessTrusted = func() bool { return false }
	axIsProcessTrustedWithOptions = nil
	screenRecordingAvailable = func() bool { return false }
	setPermissionInProgress("Accessibility", true)
	setPermissionInProgress("ScreenCapture", false)

	snapshot := CurrentPermissionSnapshot()
	if got := snapshot.Accessibility; got != PermissionStatusInProgress {
		t.Fatalf("Accessibility = %q, want %q", got, PermissionStatusInProgress)
	}
	if !strings.HasPrefix(snapshot.Message, "permission") {
		t.Fatalf("Message = %q, want onboarding text", snapshot.Message)
	}
}
