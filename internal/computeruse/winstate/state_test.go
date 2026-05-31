package winstate

import (
	"context"
	"reflect"
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
)

func TestBackendListAppsGroupsWindowsByPID(t *testing.T) {
	backend := Backend{windows: fakeWindows}
	apps, err := backend.ListApps(context.Background())
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	want := []computeruse.AppInfo{
		{Name: "calc.exe", PID: 100},
		{Name: "notepad.exe", PID: 200},
	}
	if !reflect.DeepEqual(apps, want) {
		t.Fatalf("ListApps = %#v, want %#v", apps, want)
	}
}

func TestBackendResolveAppMatchesPIDNameAndTitle(t *testing.T) {
	backend := Backend{windows: fakeWindows}
	tests := []struct {
		name     string
		selector string
		wantPID  int
	}{
		{name: "pid", selector: "100", wantPID: 100},
		{name: "process", selector: "notepad.exe", wantPID: 200},
		{name: "title substring", selector: "calc", wantPID: 100},
	}
	for _, tt := range tests {
		info, err := backend.ResolveApp(context.Background(), tt.selector)
		if err != nil {
			t.Fatalf("%s: ResolveApp: %v", tt.name, err)
		}
		if info.PID != tt.wantPID {
			t.Fatalf("%s: PID = %d, want %d", tt.name, info.PID, tt.wantPID)
		}
	}
	if _, err := backend.ResolveApp(context.Background(), "missing"); err == nil {
		t.Fatalf("ResolveApp missing = nil, want error")
	}
}

func TestBackendBuildStateReturnsWindowSnapshot(t *testing.T) {
	backend := Backend{windows: fakeWindows}
	snapshot, err := backend.BuildState(context.Background(), computeruse.StateRequest{
		App:          "calc.exe",
		Instructions: fakeInstructions{},
	})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	defer snapshot.Close()

	state := snapshot.State()
	if state.App.Name != "calc.exe" || state.App.PID != 100 {
		t.Fatalf("state.App = %#v, want calc.exe pid 100", state.App)
	}
	if state.Window.Title != "Calculator" || state.Window.Width != 300 || state.Window.Height != 200 {
		t.Fatalf("state.Window = %#v, want Calculator 300x200", state.Window)
	}
	if len(state.Tree) != 1 || state.Tree[0].Role != "Window" || state.Tree[0].Title != "Calculator" {
		t.Fatalf("state.Tree = %#v, want root window node", state.Tree)
	}
	if state.Instructions != "use calc.exe" {
		t.Fatalf("Instructions = %q, want app instructions", state.Instructions)
	}
}

func fakeWindows(context.Context) ([]Window, error) {
	return []Window{
		{
			Handle:      1,
			PID:         100,
			Title:       "Calculator",
			ProcessName: "calc.exe",
			Rect:        Rect{X: 10, Y: 20, Width: 300, Height: 200},
		},
		{
			Handle:      2,
			PID:         100,
			Title:       "Calculator Settings",
			ProcessName: "calc.exe",
			Rect:        Rect{X: 20, Y: 30, Width: 200, Height: 100},
		},
		{
			Handle:      3,
			PID:         200,
			Title:       "Notes",
			ProcessName: "notepad.exe",
			Rect:        Rect{X: 40, Y: 50, Width: 500, Height: 300},
		},
	}, nil
}

type fakeInstructions struct{}

func (fakeInstructions) Instructions(app computeruse.AppInfo) string {
	return "use " + app.Name
}
