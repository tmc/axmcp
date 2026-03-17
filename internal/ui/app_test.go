package ui

import (
	"testing"
	"time"
)

func TestWaitForAccessibilityTrust(t *testing.T) {
	oldTrusted := axIsProcessTrusted
	oldTrustedWithOptions := axIsProcessTrustedWithOptions
	defer func() {
		axIsProcessTrusted = oldTrusted
		axIsProcessTrustedWithOptions = oldTrustedWithOptions
	}()

	calls := 0
	axIsProcessTrustedWithOptions = nil
	axIsProcessTrusted = func() bool {
		calls++
		return calls >= 3
	}

	if !waitForAccessibilityTrust(500 * time.Millisecond) {
		t.Fatal("waitForAccessibilityTrust returned false, want true")
	}
	if calls < 3 {
		t.Fatalf("waitForAccessibilityTrust made %d trust checks, want at least 3", calls)
	}
}

func TestWaitForAccessibilityTrustTimeout(t *testing.T) {
	oldTrusted := axIsProcessTrusted
	oldTrustedWithOptions := axIsProcessTrustedWithOptions
	defer func() {
		axIsProcessTrusted = oldTrusted
		axIsProcessTrustedWithOptions = oldTrustedWithOptions
	}()

	axIsProcessTrustedWithOptions = nil
	axIsProcessTrusted = func() bool { return false }

	if waitForAccessibilityTrust(200 * time.Millisecond) {
		t.Fatal("waitForAccessibilityTrust returned true, want false")
	}
}
