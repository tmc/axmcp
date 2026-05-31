package computeruse

import (
	"errors"
	"runtime"
	"testing"
)

func TestPlatformUnsupportedWrapsSentinel(t *testing.T) {
	err := PlatformUnsupported("input")
	if !errors.Is(err, ErrPlatformUnsupported) {
		t.Fatalf("PlatformUnsupported error does not wrap ErrPlatformUnsupported: %v", err)
	}
	if got, want := err.Error(), "input: computer-use native desktop automation is not supported on this platform"; got != want {
		t.Fatalf("PlatformUnsupported = %q, want %q", got, want)
	}
}

func TestPlatformStatus(t *testing.T) {
	report := PlatformStatus()
	if report.OS != runtime.GOOS {
		t.Fatalf("PlatformStatus OS = %q, want %q", report.OS, runtime.GOOS)
	}
	if report.Backend == "" {
		t.Fatalf("PlatformStatus Backend is empty")
	}
	if len(report.Capabilities) == 0 {
		t.Fatalf("PlatformStatus Capabilities is empty")
	}
}
