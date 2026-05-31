package linuxstate

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
)

func TestBackendClickPointUsesXDoToolWindowLocalCoordinates(t *testing.T) {
	runner := &recordingRunner{}
	backend := Backend{run: runner.run}
	snapshot := linuxInputSnapshot()

	err := backend.ClickPoint(context.Background(), snapshot, computeruse.Point{X: 75, Y: 50}, computeruse.ClickOptions{
		Button:     "right",
		ClickCount: 2,
	})
	if err != nil {
		t.Fatalf("ClickPoint: %v", err)
	}
	want := [][]string{
		{"xdotool", "mousemove", "--window", "0x03e00007", "150", "100"},
		{"xdotool", "click", "--window", "0x03e00007", "3"},
		{"xdotool", "click", "--window", "0x03e00007", "3"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestBackendDragKeyAndTypeUseXDoToolWindow(t *testing.T) {
	runner := &recordingRunner{}
	backend := Backend{run: runner.run}
	snapshot := linuxInputSnapshot()

	if err := backend.Drag(context.Background(), snapshot, computeruse.Point{X: 0, Y: 0}, computeruse.Point{X: 149, Y: 99}, computeruse.DragOptions{}); err != nil {
		t.Fatalf("Drag: %v", err)
	}
	if err := backend.PressKey(context.Background(), snapshot, "Return"); err != nil {
		t.Fatalf("PressKey: %v", err)
	}
	index := 0
	if err := backend.TypeText(context.Background(), snapshot, &index, "-hello"); err != nil {
		t.Fatalf("TypeText: %v", err)
	}
	want := [][]string{
		{"xdotool", "mousemove", "--window", "0x03e00007", "0", "0"},
		{"xdotool", "mousedown", "--window", "0x03e00007", "1"},
		{"xdotool", "mousemove", "--window", "0x03e00007", "298", "198"},
		{"xdotool", "mouseup", "--window", "0x03e00007", "1"},
		{"xdotool", "key", "--window", "0x03e00007", "Return"},
		{"xdotool", "type", "--window", "0x03e00007", "--", "-hello"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestBackendClickElementUsesATSPIAction(t *testing.T) {
	rec := &recordingAccessibilityActions{}
	backend := Backend{atspiAction: rec.run}

	if err := backend.ClickElement(context.Background(), linuxATSPISnapshot(), 1, computeruse.ClickOptions{}); err != nil {
		t.Fatalf("ClickElement: %v", err)
	}
	want := []accessibilityAction{{
		Native: NativeElement{
			WindowID:   "0x03e00007",
			BusName:    ":1.10",
			ObjectPath: "/org/a11y/atspi/accessible/button",
		},
		Name: "click",
	}}
	if !reflect.DeepEqual(rec.actions, want) {
		t.Fatalf("actions = %#v, want %#v", rec.actions, want)
	}
}

func TestBackendPerformSecondaryActionUsesATSPIAction(t *testing.T) {
	rec := &recordingAccessibilityActions{}
	backend := Backend{atspiAction: rec.run}

	if err := backend.PerformSecondaryAction(context.Background(), linuxATSPISnapshot(), 1, "toggle"); err != nil {
		t.Fatalf("PerformSecondaryAction: %v", err)
	}
	want := []accessibilityAction{{
		Native: NativeElement{
			WindowID:   "0x03e00007",
			BusName:    ":1.10",
			ObjectPath: "/org/a11y/atspi/accessible/button",
		},
		Name: "toggle",
	}}
	if !reflect.DeepEqual(rec.actions, want) {
		t.Fatalf("actions = %#v, want %#v", rec.actions, want)
	}
}

func TestBackendElementActionsRequireATSPI(t *testing.T) {
	backend := Backend{run: (&recordingRunner{}).run}
	err := backend.ClickElement(context.Background(), linuxInputSnapshot(), 1, computeruse.ClickOptions{})
	if !errors.Is(err, computeruse.ErrPlatformUnsupported) {
		t.Fatalf("ClickElement error = %v, want ErrPlatformUnsupported", err)
	}
	err = backend.PerformSecondaryAction(context.Background(), linuxInputSnapshot(), 1, "click")
	if !errors.Is(err, computeruse.ErrPlatformUnsupported) {
		t.Fatalf("PerformSecondaryAction error = %v, want ErrPlatformUnsupported", err)
	}
	err = backend.SetValue(context.Background(), linuxInputSnapshot(), 1, "value")
	if !errors.Is(err, computeruse.ErrPlatformUnsupported) {
		t.Fatalf("SetValue error = %v, want ErrPlatformUnsupported", err)
	}
}

type recordingRunner struct {
	commands [][]string
}

func (r *recordingRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, append([]string{name}, args...))
	return nil, nil
}

type recordingAccessibilityActions struct {
	actions []accessibilityAction
}

func (r *recordingAccessibilityActions) run(_ context.Context, action accessibilityAction) error {
	r.actions = append(r.actions, action)
	return nil
}

func linuxInputSnapshot() *Snapshot {
	win := Window{
		ID:     "0x03e00007",
		PID:    999901,
		Title:  "Calculator",
		X:      10,
		Y:      20,
		Width:  300,
		Height: 200,
	}
	root := computeruse.ElementNode{Index: 0, ParentIndex: -1, Role: "Window", Title: "Calculator", Width: win.Width, Height: win.Height, Enabled: true}
	button := computeruse.ElementNode{Index: 1, ParentIndex: 0, Role: "push button", Title: "Seven", X: 20, Y: 40, Width: 50, Height: 20, Enabled: true, SecondaryActions: []string{"click"}}
	return &Snapshot{
		window: win,
		state: computeruse.AppState{
			App: computeruse.AppInfo{Name: "Calculator", PID: 999901},
			Window: computeruse.WindowInfo{
				WindowID:         0x03e00007,
				Title:            "Calculator",
				X:                win.X,
				Y:                win.Y,
				Width:            win.Width,
				Height:           win.Height,
				ScreenshotWidth:  150,
				ScreenshotHeight: 100,
			},
			Tree: []computeruse.ElementNode{root, button},
		},
		nodes: map[int]computeruse.ElementNode{
			0: root,
			1: button,
		},
		elements: map[int]NativeElement{
			0: {WindowID: win.ID},
			1: {WindowID: win.ID},
		},
	}
}

func linuxATSPISnapshot() *Snapshot {
	s := linuxInputSnapshot()
	native := NativeElement{
		WindowID:   "0x03e00007",
		BusName:    ":1.10",
		ObjectPath: "/org/a11y/atspi/accessible/button",
	}
	node := s.nodes[1]
	node.SecondaryActions = []string{"click", "toggle"}
	s.nodes[1] = node
	s.state.Tree[1] = node
	s.elements[1] = native
	return s
}
