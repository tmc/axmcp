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

func TestBackendElementActionsRequireATSPI(t *testing.T) {
	backend := Backend{run: (&recordingRunner{}).run}
	err := backend.ClickElement(context.Background(), linuxInputSnapshot(), 7, computeruse.ClickOptions{})
	if !errors.Is(err, computeruse.ErrPlatformUnsupported) {
		t.Fatalf("ClickElement error = %v, want ErrPlatformUnsupported", err)
	}
	err = backend.SetValue(context.Background(), linuxInputSnapshot(), 7, "value")
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
			Tree: []computeruse.ElementNode{{
				Index:   0,
				Role:    "Window",
				Title:   "Calculator",
				Width:   win.Width,
				Height:  win.Height,
				Enabled: true,
			}},
		},
	}
}
