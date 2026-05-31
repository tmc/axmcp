package linuxstate

import (
	"bytes"
	"context"
	"encoding/base64"
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
	if len(state.Tree) != 1 || state.Tree[0].Role != "Window" || state.Tree[0].Title != "Calculator" {
		t.Fatalf("state.Tree = %#v, want root window node", state.Tree)
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

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}
