//go:build darwin

package focusless

import "testing"

// TestIsOffSpaceUnknownWindow pins that window id 0 is not a window, and the
// answer must be an error rather than a verdict. skylight.IsWindowOffSpace
// returns (true, nil) here, which would have ax_list_windows marking bogus
// windows off-Space.
func TestIsOffSpaceUnknownWindow(t *testing.T) {
	off, err := IsOffSpace(0)
	if err == nil {
		t.Fatalf("IsOffSpace(0) = (%v, nil), want an error", off)
	}
	if off {
		t.Error("IsOffSpace(0) reported off-Space on the error path")
	}
}
