package appstate

import (
	"errors"
	"testing"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
)

func TestIsSettableRole(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{role: "AXTextField", want: true},
		{role: "AXSlider", want: true},
		{role: "AXButton", want: false},
	}
	for _, tt := range tests {
		if got := isSettableRole(tt.role); got != tt.want {
			t.Fatalf("isSettableRole(%q) = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestSnapshotStateRoundTrip(t *testing.T) {
	s := &Snapshot{
		state: computeruse.AppState{
			App: computeruse.AppInfo{Name: "Music"},
		},
	}
	if got := s.State().App.Name; got != "Music" {
		t.Fatalf("State().App.Name = %q, want Music", got)
	}
}

func TestSnapshotResolveMissing(t *testing.T) {
	s := &Snapshot{
		elements: map[int]*axuiautomation.Element{},
		nodes:    map[int]computeruse.ElementNode{},
	}
	if _, _, err := s.Resolve(1); err == nil {
		t.Fatalf("Resolve should fail for missing index")
	}
}

func TestWindowResolutionError(t *testing.T) {
	err := &WindowResolutionError{
		App:         computeruse.AppInfo{Name: "Brave Browser", BundleID: "com.brave.Browser", PID: 123},
		WindowTitle: "Smoke",
		Reason:      "no matching window found",
	}
	if got := err.Error(); got != `Brave Browser window "Smoke" unavailable: no matching window found` {
		t.Fatalf("Error() = %q", got)
	}
	var target *WindowResolutionError
	if !errors.As(err, &target) {
		t.Fatalf("errors.As failed")
	}
	if target.App.PID != 123 || target.App.BundleID != "com.brave.Browser" {
		t.Fatalf("target = %#v", target)
	}
}
