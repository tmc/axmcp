package ui

import (
	"os"
	"testing"
	"time"
)

func resetIdentityForTest(t *testing.T) {
	t.Helper()
	uiIdentity.Lock()
	defer uiIdentity.Unlock()
	uiIdentity.appName = ""
	uiIdentity.bundleID = ""
}

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

func TestIsTrustedPrefersImmediateAXSignal(t *testing.T) {
	oldTrusted := axIsProcessTrusted
	oldTrustedWithOptions := axIsProcessTrustedWithOptions
	defer func() {
		axIsProcessTrusted = oldTrusted
		axIsProcessTrustedWithOptions = oldTrustedWithOptions
	}()

	axIsProcessTrusted = func() bool { return true }
	axIsProcessTrustedWithOptions = func(uintptr) bool { return false }

	if !IsTrusted() {
		t.Fatal("IsTrusted returned false, want true when AXIsProcessTrusted is true")
	}
}

func TestConfigureIdentityOverridesFallbacks(t *testing.T) {
	resetIdentityForTest(t)
	t.Cleanup(func() {
		resetIdentityForTest(t)
		_ = os.Unsetenv("MACGO_APP_NAME")
		_ = os.Unsetenv("MACGO_BUNDLE_ID")
	})

	if err := os.Setenv("MACGO_APP_NAME", "env-name"); err != nil {
		t.Fatalf("Setenv(MACGO_APP_NAME): %v", err)
	}
	if err := os.Setenv("MACGO_BUNDLE_ID", "env.bundle"); err != nil {
		t.Fatalf("Setenv(MACGO_BUNDLE_ID): %v", err)
	}

	ConfigureIdentity("axmcp", "dev.tmc.axmcp")

	if got := uiExecName(); got != "axmcp" {
		t.Fatalf("uiExecName() = %q, want %q", got, "axmcp")
	}
	if got := uiBundleID(); got != "dev.tmc.axmcp" {
		t.Fatalf("uiBundleID() = %q, want %q", got, "dev.tmc.axmcp")
	}
}
