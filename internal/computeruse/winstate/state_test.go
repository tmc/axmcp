package winstate

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
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
	if len(state.Tree) != 1 || state.Tree[0].ParentIndex != -1 || state.Tree[0].Role != "Window" || state.Tree[0].Title != "Calculator" {
		t.Fatalf("state.Tree = %#v, want root window node", state.Tree)
	}
	if state.Tree[0].X != 0 || state.Tree[0].Y != 0 {
		t.Fatalf("root geometry = (%d,%d), want window-local origin", state.Tree[0].X, state.Tree[0].Y)
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

func TestBackendBuildStateUsesAutomationTree(t *testing.T) {
	backend := Backend{
		windows:    fakeWindows,
		screenshot: fakeScreenshot(320, 200),
		automation: func(_ context.Context, win Window) (AutomationNode, error) {
			return AutomationNode{
				Native: NativeElement{
					WindowHandle:     win.Handle,
					AutomationHandle: 10,
				},
				Role:    "Window",
				Title:   win.Title,
				Rect:    win.Rect,
				Enabled: true,
				Children: []AutomationNode{
					{
						Native: NativeElement{
							WindowHandle:     win.Handle,
							AutomationHandle: 11,
						},
						Role:             "Button",
						Title:            "Seven",
						Rect:             Rect{X: 20, Y: 40, Width: 50, Height: 20},
						Enabled:          true,
						SecondaryActions: []string{"invoke"},
					},
					{
						Native: NativeElement{
							WindowHandle:     win.Handle,
							AutomationHandle: 12,
						},
						Role:       "Edit",
						Value:      "42",
						Identifier: "display",
						Rect:       Rect{X: 30, Y: 70, Width: 100, Height: 30},
						Enabled:    true,
						Settable:   true,
						Children: []AutomationNode{{
							Native: NativeElement{
								WindowHandle:     win.Handle,
								AutomationHandle: 13,
							},
							Role:    "Text",
							Title:   "result",
							Rect:    Rect{X: 35, Y: 75, Width: 40, Height: 10},
							Enabled: true,
						}},
					},
				},
			}, nil
		},
	}
	snapshot, err := backend.BuildState(context.Background(), computeruse.StateRequest{App: "calc.exe"})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	state := snapshot.State()
	want := []computeruse.ElementNode{
		{Index: 0, ParentIndex: -1, Role: "Window", Title: "Calculator", X: 0, Y: 0, Width: 300, Height: 200, Enabled: true},
		{Index: 1, ParentIndex: 0, Role: "Button", Title: "Seven", X: 10, Y: 20, Width: 50, Height: 20, Enabled: true, SecondaryActions: []string{"invoke"}},
		{Index: 2, ParentIndex: 0, Role: "Edit", Value: "42", Identifier: "display", X: 20, Y: 50, Width: 100, Height: 30, Enabled: true, Settable: true},
		{Index: 3, ParentIndex: 2, Role: "Text", Title: "result", X: 25, Y: 55, Width: 40, Height: 10, Enabled: true},
	}
	if !reflect.DeepEqual(state.Tree, want) {
		t.Fatalf("state.Tree = %#v, want %#v", state.Tree, want)
	}
	winSnapshot, ok := snapshot.(*Snapshot)
	if !ok {
		t.Fatalf("snapshot = %T, want *Snapshot", snapshot)
	}
	native, node, err := winSnapshot.NativeElement(2)
	if err != nil {
		t.Fatalf("NativeElement: %v", err)
	}
	if native.WindowHandle != 1 || native.AutomationHandle != 12 {
		t.Fatalf("native element = %#v, want hwnd 1 automation 12", native)
	}
	if !reflect.DeepEqual(node, want[2]) {
		t.Fatalf("node = %#v, want %#v", node, want[2])
	}
	if _, _, err := winSnapshot.NativeElement(99); err == nil {
		t.Fatalf("NativeElement missing = nil, want error")
	}
}

func TestBackendBuildStateReportsInjectedAutomationError(t *testing.T) {
	backend := Backend{
		windows:    fakeWindows,
		screenshot: fakeScreenshot(320, 200),
		automation: func(context.Context, Window) (AutomationNode, error) {
			return AutomationNode{}, errFakeAutomation
		},
	}
	if _, err := backend.BuildState(context.Background(), computeruse.StateRequest{App: "calc.exe"}); err == nil {
		t.Fatalf("BuildState automation error = nil, want error")
	}
}

func TestSnapshotCloseReleasesAutomationTree(t *testing.T) {
	var releases int
	backend := Backend{
		windows:    fakeWindows,
		screenshot: fakeScreenshot(320, 200),
		automation: func(_ context.Context, win Window) (AutomationNode, error) {
			return AutomationNode{
				Native:  NativeElement{WindowHandle: win.Handle, AutomationHandle: 10},
				Role:    "Window",
				Title:   win.Title,
				Rect:    win.Rect,
				Enabled: true,
				release: func() { releases++ },
				Children: []AutomationNode{{
					Native:  NativeElement{WindowHandle: win.Handle, AutomationHandle: 11},
					Role:    "Button",
					Title:   "Seven",
					Rect:    Rect{X: 20, Y: 40, Width: 50, Height: 20},
					Enabled: true,
					release: func() { releases++ },
				}},
			}, nil
		},
	}
	snapshot, err := backend.BuildState(context.Background(), computeruse.StateRequest{App: "calc.exe"})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	if releases != 0 {
		t.Fatalf("releases before Close = %d, want 0", releases)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if releases != 2 {
		t.Fatalf("releases = %d, want 2", releases)
	}
}

func TestBackendBuildStateReleasesAutomationTreeOnScreenshotError(t *testing.T) {
	var released bool
	backend := Backend{
		windows: fakeWindows,
		screenshot: func(context.Context, Window) ([]byte, error) {
			return nil, errFakeScreenshot
		},
		automation: func(_ context.Context, win Window) (AutomationNode, error) {
			return AutomationNode{
				Native:  NativeElement{WindowHandle: win.Handle, AutomationHandle: 10},
				Role:    "Window",
				Title:   win.Title,
				Rect:    win.Rect,
				Enabled: true,
				release: func() { released = true },
			}, nil
		},
	}
	if _, err := backend.BuildState(context.Background(), computeruse.StateRequest{App: "calc.exe"}); !errors.Is(err, errFakeScreenshot) {
		t.Fatalf("BuildState error = %v, want %v", err, errFakeScreenshot)
	}
	if !released {
		t.Fatalf("automation tree was not released")
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

var errFakeAutomation = errors.New("fake automation tree")
var errFakeScreenshot = errors.New("fake screenshot")

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
