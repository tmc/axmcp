//go:build darwin

package spacedetect

import (
	"errors"
	"os"
	"testing"
)

func TestIsOffSpaceUnknownWindow(t *testing.T) {
	if os.Getenv("SKIPSKYLIGHT") == "1" {
		t.Skip("SKIPSKYLIGHT=1")
	}

	// Window ID 0 is not a real window. Either SkyLight is unavailable
	// (ErrSkyLightUnavailable) or the lookup yields no Space membership;
	// both are valid graceful-failure outcomes. The contract is "do not
	// crash, do not falsely claim on-Space".
	off, err := IsOffSpace(0)
	if err == nil {
		t.Fatalf("IsOffSpace(0): expected error, got off=%v", off)
	}
	if off {
		t.Fatalf("IsOffSpace(0): off must be false on error path, got true")
	}

	// When SkyLight failed to load, the returned error must wrap
	// ErrSkyLightUnavailable so callers can branch via errors.Is.
	// On systems where SkyLight loaded fine, windowID=0 instead returns
	// a non-wrapped lookup error; that case is fine — we just don't get
	// to exercise the wrap path here.
	if errors.Is(err, ErrSkyLightUnavailable) {
		t.Logf("SkyLight unavailable: %v", err)
	}
}

func TestErrSkyLightUnavailableUnwraps(t *testing.T) {
	wrapped := errors.New("dummy")
	chained := errors.Join(ErrSkyLightUnavailable, wrapped)
	if !errors.Is(chained, ErrSkyLightUnavailable) {
		t.Fatalf("errors.Is(chained, ErrSkyLightUnavailable) = false, want true")
	}
}
