package linuxstate

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"reflect"
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
)

const wmctrlOutput = `0x03e00007  0 999901 10 20 300 200 host Calculator
0x03e00008  0 999901 20 30 200 100 host Calculator Settings
0x04a00002  1 999902 40 50 500 300 host Notes - notepad
`

func TestParseWMCTRL(t *testing.T) {
	windows, err := parseWMCTRL([]byte(wmctrlOutput))
	if err != nil {
		t.Fatalf("parseWMCTRL: %v", err)
	}
	if len(windows) != 3 {
		t.Fatalf("windows = %d, want 3", len(windows))
	}
	got := windows[0]
	want := Window{
		ID:      "0x03e00007",
		Desktop: "0",
		PID:     999901,
		Title:   "Calculator",
		X:       10,
		Y:       20,
		Width:   300,
		Height:  200,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first window = %#v, want %#v", got, want)
	}
}

func TestBackendListAppsGroupsWindowsByPID(t *testing.T) {
	backend := Backend{run: fakeWMCTRL}
	apps, err := backend.ListApps(context.Background())
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	want := []computeruse.AppInfo{
		{Name: "Calculator", PID: 999901},
		{Name: "Notes - notepad", PID: 999902},
	}
	if !reflect.DeepEqual(apps, want) {
		t.Fatalf("ListApps = %#v, want %#v", apps, want)
	}
}

func TestBackendResolveAppMatchesPIDTitleAndWindowID(t *testing.T) {
	backend := Backend{run: fakeWMCTRL}
	tests := []struct {
		name     string
		selector string
		wantPID  int
	}{
		{name: "pid", selector: "999901", wantPID: 999901},
		{name: "title substring", selector: "note", wantPID: 999902},
		{name: "window id", selector: "0x04a00002", wantPID: 999902},
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
	runner := fakeRunner{png: testPNG(t, 320, 200)}
	backend := Backend{run: runner.run}
	snapshot, err := backend.BuildState(context.Background(), computeruse.StateRequest{
		App:          "calculator",
		Instructions: fakeInstructions{},
	})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	defer snapshot.Close()

	state := snapshot.State()
	if state.App.Name != "Calculator" || state.App.PID != 999901 {
		t.Fatalf("state.App = %#v, want Calculator pid 999901", state.App)
	}
	if state.Window.Title != "Calculator" || state.Window.Width != 300 || state.Window.Height != 200 {
		t.Fatalf("state.Window = %#v, want Calculator 300x200", state.Window)
	}
	if state.Window.ScreenshotWidth != 320 || state.Window.ScreenshotHeight != 200 {
		t.Fatalf("screenshot size = %dx%d, want 320x200", state.Window.ScreenshotWidth, state.Window.ScreenshotHeight)
	}
	if _, err := base64.StdEncoding.DecodeString(state.ScreenshotPNGBase64); err != nil {
		t.Fatalf("ScreenshotPNGBase64 is not base64 PNG: %v", err)
	}
	if !runner.imported {
		t.Fatalf("BuildState did not capture screenshot with import")
	}
	if len(state.Tree) != 1 || state.Tree[0].ParentIndex != -1 || state.Tree[0].Role != "Window" || state.Tree[0].Title != "Calculator" {
		t.Fatalf("state.Tree = %#v, want root window node", state.Tree)
	}
	if state.Tree[0].X != 0 || state.Tree[0].Y != 0 {
		t.Fatalf("root geometry = (%d,%d), want window-local origin", state.Tree[0].X, state.Tree[0].Y)
	}
	if state.Instructions != "use Calculator" {
		t.Fatalf("Instructions = %q, want app instructions", state.Instructions)
	}
}

func TestBackendBuildStateCapsScreenshotLongSide(t *testing.T) {
	runner := fakeRunner{png: testPNG(t, 3136, 1960)}
	backend := Backend{run: runner.run}
	snapshot, err := backend.BuildState(context.Background(), computeruse.StateRequest{App: "calculator"})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	state := snapshot.State()
	if state.Window.ScreenshotWidth != 1568 || state.Window.ScreenshotHeight != 980 {
		t.Fatalf("screenshot size = %dx%d, want 1568x980", state.Window.ScreenshotWidth, state.Window.ScreenshotHeight)
	}
	data, err := base64.StdEncoding.DecodeString(state.ScreenshotPNGBase64)
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if cfg.Width != 1568 || cfg.Height != 980 {
		t.Fatalf("encoded screenshot = %dx%d, want 1568x980", cfg.Width, cfg.Height)
	}
}

func TestBackendBuildStateUsesAccessibilityTree(t *testing.T) {
	runner := fakeRunner{png: testPNG(t, 320, 200)}
	backend := Backend{
		run: runner.run,
		accessibility: func(_ context.Context, win Window) (AccessibilityNode, error) {
			return AccessibilityNode{
				Native: NativeElement{
					WindowID:   win.ID,
					ObjectPath: "/org/a11y/atspi/accessible/root",
				},
				Role:    "Window",
				Title:   win.Title,
				Rect:    Rect{X: win.X, Y: win.Y, Width: win.Width, Height: win.Height},
				Enabled: true,
				Children: []AccessibilityNode{
					{
						Native: NativeElement{
							WindowID:   win.ID,
							ObjectPath: "/org/a11y/atspi/accessible/button",
						},
						Role:             "push button",
						Title:            "Seven",
						Rect:             Rect{X: 20, Y: 40, Width: 50, Height: 20},
						Enabled:          true,
						SecondaryActions: []string{"click"},
					},
					{
						Native: NativeElement{
							WindowID:   win.ID,
							ObjectPath: "/org/a11y/atspi/accessible/text",
						},
						Role:       "text",
						Value:      "42",
						Identifier: "display",
						Rect:       Rect{X: 30, Y: 70, Width: 100, Height: 30},
						Enabled:    true,
						Settable:   true,
					},
				},
			}, nil
		},
	}
	snapshot, err := backend.BuildState(context.Background(), computeruse.StateRequest{App: "calculator"})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	state := snapshot.State()
	want := []computeruse.ElementNode{
		{Index: 0, ParentIndex: -1, Role: "Window", Title: "Calculator", X: 0, Y: 0, Width: 300, Height: 200, Enabled: true},
		{Index: 1, ParentIndex: 0, Role: "push button", Title: "Seven", X: 10, Y: 20, Width: 50, Height: 20, Enabled: true, SecondaryActions: []string{"click"}},
		{Index: 2, ParentIndex: 0, Role: "text", Value: "42", Identifier: "display", X: 20, Y: 50, Width: 100, Height: 30, Enabled: true, Settable: true},
	}
	if !reflect.DeepEqual(state.Tree, want) {
		t.Fatalf("state.Tree = %#v, want %#v", state.Tree, want)
	}
	linuxSnapshot, ok := snapshot.(*Snapshot)
	if !ok {
		t.Fatalf("snapshot = %T, want *Snapshot", snapshot)
	}
	native, node, err := linuxSnapshot.NativeElement(2)
	if err != nil {
		t.Fatalf("NativeElement: %v", err)
	}
	if native.WindowID != "0x03e00007" || native.ObjectPath != "/org/a11y/atspi/accessible/text" {
		t.Fatalf("native element = %#v, want text object path", native)
	}
	if !reflect.DeepEqual(node, want[2]) {
		t.Fatalf("node = %#v, want %#v", node, want[2])
	}
	if _, _, err := linuxSnapshot.NativeElement(99); err == nil {
		t.Fatalf("NativeElement missing = nil, want error")
	}
}

func TestBackendBuildStateReportsInjectedAccessibilityError(t *testing.T) {
	runner := fakeRunner{png: testPNG(t, 320, 200)}
	backend := Backend{
		run: runner.run,
		accessibility: func(context.Context, Window) (AccessibilityNode, error) {
			return AccessibilityNode{}, errFakeAccessibility
		},
	}
	if _, err := backend.BuildState(context.Background(), computeruse.StateRequest{App: "calculator"}); err == nil {
		t.Fatalf("BuildState accessibility error = nil, want error")
	}
}

func fakeWMCTRL(context.Context, string, ...string) ([]byte, error) {
	return []byte(wmctrlOutput), nil
}

type fakeRunner struct {
	png      []byte
	imported bool
}

func (r *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "wmctrl":
		return []byte(wmctrlOutput), nil
	case "import":
		if !reflect.DeepEqual(args, []string{"-window", "0x03e00007", "png:-"}) {
			return nil, fmt.Errorf("import args = %#v", args)
		}
		r.imported = true
		return r.png, nil
	default:
		return nil, fmt.Errorf("unexpected command %q", name)
	}
}

type fakeInstructions struct{}

func (fakeInstructions) Instructions(app computeruse.AppInfo) string {
	return "use " + app.Name
}

var errFakeAccessibility = errors.New("fake accessibility tree")

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}
