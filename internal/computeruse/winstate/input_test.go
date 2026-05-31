package winstate

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/coords"
)

func TestBackendClickPointUsesWindowScreenshotCoordinates(t *testing.T) {
	rec := &recordingInput{}
	backend := Backend{input: rec.run}

	err := backend.ClickPoint(context.Background(), winInputSnapshot(), computeruse.Point{X: 75, Y: 50}, computeruse.ClickOptions{
		Button:     "right",
		ClickCount: 2,
	})
	if err != nil {
		t.Fatalf("ClickPoint: %v", err)
	}
	want := []inputAction{{
		Kind:       inputClick,
		Target:     1,
		Button:     mouseRight,
		ClickCount: 2,
		Start:      coords.ScreenPoint{X: 160, Y: 120},
	}}
	if !reflect.DeepEqual(rec.actions, want) {
		t.Fatalf("actions = %#v, want %#v", rec.actions, want)
	}
}

func TestBackendDragUsesWindowScreenshotCoordinates(t *testing.T) {
	rec := &recordingInput{}
	backend := Backend{input: rec.run}

	err := backend.Drag(context.Background(), winInputSnapshot(), computeruse.Point{X: 0, Y: 0}, computeruse.Point{X: 149, Y: 99}, computeruse.DragOptions{})
	if err != nil {
		t.Fatalf("Drag: %v", err)
	}
	want := []inputAction{{
		Kind:   inputDrag,
		Target: 1,
		Button: mouseLeft,
		Start:  coords.ScreenPoint{X: 10, Y: 20},
		End:    coords.ScreenPoint{X: 308, Y: 218},
	}}
	if !reflect.DeepEqual(rec.actions, want) {
		t.Fatalf("actions = %#v, want %#v", rec.actions, want)
	}
}

func TestBackendClickElementUsesNativeWindowHandle(t *testing.T) {
	rec := &recordingInput{}
	backend := Backend{input: rec.run}

	if err := backend.ClickElement(context.Background(), winInputSnapshot(), 1, computeruse.ClickOptions{}); err != nil {
		t.Fatalf("ClickElement: %v", err)
	}
	want := []inputAction{{
		Kind:       inputClick,
		Target:     77,
		Button:     mouseLeft,
		ClickCount: 1,
		Start:      coords.ScreenPoint{X: 55, Y: 70},
	}}
	if !reflect.DeepEqual(rec.actions, want) {
		t.Fatalf("actions = %#v, want %#v", rec.actions, want)
	}
}

func TestBackendClickElementPrefersInvokePattern(t *testing.T) {
	rec := &recordingAutomation{}
	backend := Backend{uiaAction: rec.run}
	snapshot := winInputSnapshot()
	node := snapshot.nodes[1]
	node.SecondaryActions = []string{"invoke"}
	snapshot.nodes[1] = node
	snapshot.state.Tree[1].SecondaryActions = []string{"invoke"}

	if err := backend.ClickElement(context.Background(), snapshot, 1, computeruse.ClickOptions{}); err != nil {
		t.Fatalf("ClickElement: %v", err)
	}
	want := []automationAction{{
		Kind:    automationInvoke,
		Element: 1234,
	}}
	if !reflect.DeepEqual(rec.actions, want) {
		t.Fatalf("actions = %#v, want %#v", rec.actions, want)
	}
}

func TestBackendPerformSecondaryActionUsesUIAPattern(t *testing.T) {
	rec := &recordingAutomation{}
	backend := Backend{uiaAction: rec.run}

	if err := backend.PerformSecondaryAction(context.Background(), winInputSnapshot(), 1, "toggle"); err != nil {
		t.Fatalf("PerformSecondaryAction: %v", err)
	}
	want := []automationAction{{
		Kind:    automationToggle,
		Element: 1234,
	}}
	if !reflect.DeepEqual(rec.actions, want) {
		t.Fatalf("actions = %#v, want %#v", rec.actions, want)
	}
}

func TestBackendSetValueUsesUIAPattern(t *testing.T) {
	rec := &recordingAutomation{}
	backend := Backend{uiaAction: rec.run}

	if err := backend.SetValue(context.Background(), winInputSnapshot(), 1, "42"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	want := []automationAction{{
		Kind:    automationSetValue,
		Element: 1234,
		Value:   "42",
	}}
	if !reflect.DeepEqual(rec.actions, want) {
		t.Fatalf("actions = %#v, want %#v", rec.actions, want)
	}
}

func TestParseAutomationAction(t *testing.T) {
	tests := []struct {
		action string
		want   automationActionKind
	}{
		{action: "invoke", want: automationInvoke},
		{action: "click", want: automationInvoke},
		{action: "toggle", want: automationToggle},
		{action: "select", want: automationSelect},
		{action: "expand", want: automationExpand},
		{action: "collapse", want: automationCollapse},
		{action: "expand_collapse", want: automationExpandCollapse},
	}
	for _, tt := range tests {
		got, err := parseAutomationAction(tt.action)
		if err != nil {
			t.Fatalf("parseAutomationAction(%q): %v", tt.action, err)
		}
		if got != tt.want {
			t.Fatalf("parseAutomationAction(%q) = %v, want %v", tt.action, got, tt.want)
		}
	}
}

func TestBackendWindowsInputUnsupportedPaths(t *testing.T) {
	backend := Backend{input: (&recordingInput{}).run}
	err := backend.ClickPoint(context.Background(), winInputSnapshot(), computeruse.Point{}, computeruse.ClickOptions{ForegroundHID: true})
	if !errors.Is(err, computeruse.ErrPlatformUnsupported) {
		t.Fatalf("ForegroundHID error = %v, want ErrPlatformUnsupported", err)
	}
	err = backend.SetValue(context.Background(), winInputSnapshot(), 0, "value")
	if !errors.Is(err, computeruse.ErrPlatformUnsupported) {
		t.Fatalf("SetValue error = %v, want ErrPlatformUnsupported", err)
	}
	err = backend.PressKey(context.Background(), winInputSnapshot(), "Return")
	if !errors.Is(err, computeruse.ErrPlatformUnsupported) {
		t.Fatalf("PressKey error = %v, want ErrPlatformUnsupported", err)
	}
}

type recordingInput struct {
	actions []inputAction
}

func (r *recordingInput) run(_ context.Context, action inputAction) error {
	r.actions = append(r.actions, action)
	return nil
}

type recordingAutomation struct {
	actions []automationAction
}

func (r *recordingAutomation) run(_ context.Context, action automationAction) error {
	r.actions = append(r.actions, action)
	return nil
}

func winInputSnapshot() *Snapshot {
	win := Window{
		Handle:      1,
		PID:         999901,
		Title:       "Calculator",
		ProcessName: "calc.exe",
		Rect:        Rect{X: 10, Y: 20, Width: 300, Height: 200},
	}
	return &Snapshot{
		window: win,
		state: computeruse.AppState{
			App: computeruse.AppInfo{Name: "calc.exe", PID: 999901},
			Window: computeruse.WindowInfo{
				WindowID:         1,
				Title:            "Calculator",
				X:                win.Rect.X,
				Y:                win.Rect.Y,
				Width:            win.Rect.Width,
				Height:           win.Rect.Height,
				ScreenshotWidth:  150,
				ScreenshotHeight: 100,
			},
			Tree: []computeruse.ElementNode{
				{Index: 0, ParentIndex: -1, Role: "Window", Title: "Calculator", Width: 300, Height: 200, Enabled: true},
				{Index: 1, ParentIndex: 0, Role: "Button", Title: "Seven", X: 20, Y: 40, Width: 50, Height: 20, Enabled: true},
			},
		},
		nodes: map[int]computeruse.ElementNode{
			0: {Index: 0, ParentIndex: -1, Role: "Window", Title: "Calculator", Width: 300, Height: 200, Enabled: true},
			1: {Index: 1, ParentIndex: 0, Role: "Button", Title: "Seven", X: 20, Y: 40, Width: 50, Height: 20, Enabled: true},
		},
		elements: map[int]NativeElement{
			0: {WindowHandle: 1},
			1: {WindowHandle: 77, AutomationHandle: 1234},
		},
	}
}
