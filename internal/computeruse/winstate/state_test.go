package winstate

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
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
	backend := Backend{windows: fakeWindows, screenshot: fakeScreenshot(320, 200)}
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
	if state.Window.ScreenshotWidth != 320 || state.Window.ScreenshotHeight != 200 {
		t.Fatalf("screenshot size = %dx%d, want 320x200", state.Window.ScreenshotWidth, state.Window.ScreenshotHeight)
	}
	if _, err := base64.StdEncoding.DecodeString(state.ScreenshotPNGBase64); err != nil {
		t.Fatalf("ScreenshotPNGBase64 is not base64 PNG: %v", err)
	}
	if len(state.Tree) != 1 || state.Tree[0].Role != "Window" || state.Tree[0].Title != "Calculator" {
		t.Fatalf("state.Tree = %#v, want root window node", state.Tree)
	}
	if state.Instructions != "use calc.exe" {
		t.Fatalf("Instructions = %q, want app instructions", state.Instructions)
	}
}

func TestBackendBuildStateCapsScreenshotLongSide(t *testing.T) {
	backend := Backend{windows: fakeWindows, screenshot: fakeScreenshot(3136, 1960)}
	snapshot, err := backend.BuildState(context.Background(), computeruse.StateRequest{App: "calc.exe"})
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

func fakeScreenshot(width, height int) func(context.Context, Window) ([]byte, error) {
	return func(context.Context, Window) ([]byte, error) {
		return testPNG(width, height)
	}
}

func testPNG(width, height int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
