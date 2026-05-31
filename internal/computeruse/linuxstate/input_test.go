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

func TestBackendClickPointUsesForegroundHID(t *testing.T) {
	runner := &recordingRunner{}
	backend := Backend{run: runner.run}

	err := backend.ClickPoint(context.Background(), linuxInputSnapshot(), computeruse.Point{X: 75, Y: 50}, computeruse.ClickOptions{
		Button:        "right",
		ClickCount:    2,
		ForegroundHID: true,
	})
	if err != nil {
		t.Fatalf("ClickPoint: %v", err)
	}
	want := [][]string{
		{"xdotool", "windowactivate", "--sync", "0x03e00007"},
		{"xdotool", "mousemove", "160", "120"},
		{"xdotool", "click", "3"},
		{"xdotool", "click", "3"},
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

func TestBackendTypeTextElementFocusesThenTypes(t *testing.T) {
	runner := &recordingRunner{}
	backend := Backend{run: runner.run}
	index := 2

	if err := backend.TypeText(context.Background(), linuxInputSnapshot(), &index, "99"); err != nil {
		t.Fatalf("TypeText: %v", err)
	}
	want := [][]string{
		{"xdotool", "mousemove", "--window", "0x03e00007", "80", "85"},
		{"xdotool", "click", "--window", "0x03e00007", "1"},
		{"xdotool", "type", "--window", "0x03e00007", "--", "99"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestBackendScrollElementUsesXDoToolWindow(t *testing.T) {
	runner := &recordingRunner{}
	backend := Backend{run: runner.run}

	if err := backend.ScrollElement(context.Background(), linuxInputSnapshot(), 1, computeruse.ScrollOptions{
		Direction: "up",
		Pages:     0.4,
	}); err != nil {
		t.Fatalf("ScrollElement: %v", err)
	}
	want := [][]string{
		{"xdotool", "mousemove", "--window", "0x03e00007", "45", "50"},
		{"xdotool", "click", "--window", "0x03e00007", "4"},
		{"xdotool", "click", "--window", "0x03e00007", "4"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestBackendScrollRootUsesXDoToolWindowCenter(t *testing.T) {
	runner := &recordingRunner{}
	backend := Backend{run: runner.run}

	if err := backend.ScrollElement(context.Background(), linuxInputSnapshot(), 0, computeruse.ScrollOptions{
		Direction: "right",
	}); err != nil {
		t.Fatalf("ScrollElement: %v", err)
	}
	want := [][]string{
		{"xdotool", "mousemove", "--window", "0x03e00007", "150", "100"},
		{"xdotool", "click", "--window", "0x03e00007", "7"},
		{"xdotool", "click", "--window", "0x03e00007", "7"},
		{"xdotool", "click", "--window", "0x03e00007", "7"},
		{"xdotool", "click", "--window", "0x03e00007", "7"},
		{"xdotool", "click", "--window", "0x03e00007", "7"},
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

func TestBackendClickElementFallsBackToXDoToolGeometry(t *testing.T) {
	runner := &recordingRunner{}
	backend := Backend{run: runner.run}

	if err := backend.ClickElement(context.Background(), linuxInputSnapshot(), 1, computeruse.ClickOptions{
		Button:     "right",
		ClickCount: 2,
	}); err != nil {
		t.Fatalf("ClickElement: %v", err)
	}
	want := [][]string{
		{"xdotool", "mousemove", "--window", "0x03e00007", "45", "50"},
		{"xdotool", "click", "--window", "0x03e00007", "3"},
		{"xdotool", "click", "--window", "0x03e00007", "3"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestBackendClickElementFallsBackWhenATSPIUnavailable(t *testing.T) {
	runner := &recordingRunner{}
	rec := &recordingAccessibilityActions{err: computeruse.PlatformUnsupported("perform AT-SPI action")}
	backend := Backend{run: runner.run, atspiAction: rec.run}

	if err := backend.ClickElement(context.Background(), linuxATSPISnapshot(), 1, computeruse.ClickOptions{}); err != nil {
		t.Fatalf("ClickElement: %v", err)
	}
	wantActions := []accessibilityAction{{
		Native: NativeElement{
			WindowID:   "0x03e00007",
			BusName:    ":1.10",
			ObjectPath: "/org/a11y/atspi/accessible/button",
		},
		Name: "click",
	}}
	if !reflect.DeepEqual(rec.actions, wantActions) {
		t.Fatalf("actions = %#v, want %#v", rec.actions, wantActions)
	}
	wantCommands := [][]string{
		{"xdotool", "mousemove", "--window", "0x03e00007", "45", "50"},
		{"xdotool", "click", "--window", "0x03e00007", "1"},
	}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, wantCommands)
	}
}

func TestBackendClickElementUsesForegroundHIDFallback(t *testing.T) {
	runner := &recordingRunner{}
	backend := Backend{run: runner.run}

	if err := backend.ClickElement(context.Background(), linuxInputSnapshot(), 1, computeruse.ClickOptions{
		ForegroundHID: true,
	}); err != nil {
		t.Fatalf("ClickElement: %v", err)
	}
	want := [][]string{
		{"xdotool", "windowactivate", "--sync", "0x03e00007"},
		{"xdotool", "mousemove", "55", "70"},
		{"xdotool", "click", "1"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
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

func TestBackendSetValueUsesATSPIValue(t *testing.T) {
	rec := &recordingAccessibilityValues{}
	backend := Backend{atspiSetValue: rec.run}

	if err := backend.SetValue(context.Background(), linuxATSPISnapshot(), 2, "49"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	want := []accessibilityValue{{
		Native: NativeElement{
			WindowID:   "0x03e00007",
			BusName:    ":1.10",
			ObjectPath: "/org/a11y/atspi/accessible/text",
		},
		Value: "49",
	}}
	if !reflect.DeepEqual(rec.values, want) {
		t.Fatalf("values = %#v, want %#v", rec.values, want)
	}
}

func TestBackendElementActionsRequireATSPI(t *testing.T) {
	backend := Backend{run: (&recordingRunner{}).run}
	err := backend.PerformSecondaryAction(context.Background(), linuxInputSnapshot(), 1, "click")
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
	err     error
}

func (r *recordingAccessibilityActions) run(_ context.Context, action accessibilityAction) error {
	r.actions = append(r.actions, action)
	return r.err
}

type recordingAccessibilityValues struct {
	values []accessibilityValue
}

func (r *recordingAccessibilityValues) run(_ context.Context, value accessibilityValue) error {
	r.values = append(r.values, value)
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
	text := computeruse.ElementNode{Index: 2, ParentIndex: 0, Role: "text", Title: "Display", Value: "42", X: 30, Y: 70, Width: 100, Height: 30, Enabled: true, Settable: true}
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
			Tree: []computeruse.ElementNode{root, button, text},
		},
		nodes: map[int]computeruse.ElementNode{
			0: root,
			1: button,
			2: text,
		},
		elements: map[int]NativeElement{
			0: {WindowID: win.ID},
			1: {WindowID: win.ID},
			2: {WindowID: win.ID},
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
	s.elements[2] = NativeElement{
		WindowID:   "0x03e00007",
		BusName:    ":1.10",
		ObjectPath: "/org/a11y/atspi/accessible/text",
	}
	return s
}
