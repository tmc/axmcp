package appstate

import (
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
