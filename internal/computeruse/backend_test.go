package computeruse

import (
	"context"
	"errors"
	"testing"
)

func TestUnsupportedBackendReportsSentinel(t *testing.T) {
	backend := NewUnsupportedBackend(PlatformReport{
		OS:      "plan9",
		Backend: "unsupported",
	})

	if got := backend.Platform().Backend; got != "unsupported" {
		t.Fatalf("Platform Backend = %q, want unsupported", got)
	}

	checks := []struct {
		name string
		run  func() error
	}{
		{"list apps", func() error {
			_, err := backend.State().ListApps(context.Background())
			return err
		}},
		{"build state", func() error {
			_, err := backend.State().BuildState(context.Background(), StateRequest{App: "Calculator"})
			return err
		}},
		{"click point", func() error {
			return backend.Input().ClickPoint(context.Background(), nil, Point{X: 1, Y: 2}, ClickOptions{})
		}},
		{"capture window", func() error {
			_, err := backend.Screenshots().CaptureWindow(context.Background(), ScreenshotRequest{})
			return err
		}},
		{"intervention", func() error {
			_, err := backend.Intervention().Status(context.Background())
			return err
		}},
	}

	for _, tt := range checks {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if !errors.Is(err, ErrPlatformUnsupported) {
				t.Fatalf("error = %v, want ErrPlatformUnsupported", err)
			}
		})
	}
}

func TestBackendContractCanBindFakeImplementation(t *testing.T) {
	var backend Backend = fakeBackend{}
	snapshot, err := backend.State().BuildState(context.Background(), StateRequest{
		App: "Calculator",
	})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	defer snapshot.Close()

	state := snapshot.State()
	if state.App.Name != "Calculator" {
		t.Fatalf("state.App.Name = %q, want Calculator", state.App.Name)
	}
	if err := backend.Input().ClickPoint(context.Background(), snapshot, Point{X: 10, Y: 20}, ClickOptions{}); err != nil {
		t.Fatalf("ClickPoint: %v", err)
	}
}

type fakeBackend struct{}

func (fakeBackend) Platform() PlatformReport {
	return PlatformReport{OS: "test", Backend: "fake", NativeDesktopAutomation: true}
}
func (fakeBackend) State() StateBackend               { return fakeStateBackend{} }
func (fakeBackend) Input() InputBackend               { return fakeInputBackend{} }
func (fakeBackend) Screenshots() ScreenshotBackend    { return fakeScreenshotBackend{} }
func (fakeBackend) Intervention() InterventionBackend { return fakeInterventionBackend{} }

type fakeSnapshot struct {
	state AppState
}

func (s *fakeSnapshot) State() AppState { return s.state }
func (s *fakeSnapshot) Close() error    { return nil }

type fakeStateBackend struct{}

func (fakeStateBackend) ListApps(context.Context) ([]AppInfo, error) {
	return []AppInfo{{Name: "Calculator"}}, nil
}

func (fakeStateBackend) ResolveApp(context.Context, string) (AppInfo, error) {
	return AppInfo{Name: "Calculator"}, nil
}

func (fakeStateBackend) BuildState(_ context.Context, req StateRequest) (Snapshot, error) {
	return &fakeSnapshot{state: AppState{
		App:    AppInfo{Name: req.App},
		Window: WindowInfo{Width: 100, Height: 100, ScreenshotWidth: 100, ScreenshotHeight: 100},
	}}, nil
}

type fakeInputBackend struct{}

func (fakeInputBackend) ClickElement(context.Context, Snapshot, int, ClickOptions) error {
	return nil
}
func (fakeInputBackend) ClickPoint(context.Context, Snapshot, Point, ClickOptions) error {
	return nil
}
func (fakeInputBackend) Drag(context.Context, Snapshot, Point, Point, DragOptions) error {
	return nil
}
func (fakeInputBackend) ScrollElement(context.Context, Snapshot, int, ScrollOptions) error {
	return nil
}
func (fakeInputBackend) PerformSecondaryAction(context.Context, Snapshot, int, string) error {
	return nil
}
func (fakeInputBackend) SetValue(context.Context, Snapshot, int, string) error {
	return nil
}
func (fakeInputBackend) PressKey(context.Context, Snapshot, string) error {
	return nil
}
func (fakeInputBackend) TypeText(context.Context, Snapshot, *int, string) error {
	return nil
}

type fakeScreenshotBackend struct{}

func (fakeScreenshotBackend) CaptureWindow(context.Context, ScreenshotRequest) (Screenshot, error) {
	return Screenshot{PNG: []byte("png"), Width: 1, Height: 1}, nil
}

type fakeInterventionBackend struct{}

func (fakeInterventionBackend) Start() error { return nil }
func (fakeInterventionBackend) Close() error { return nil }
func (fakeInterventionBackend) Status(context.Context) (InterventionStatus, error) {
	return InterventionStatus{Enabled: true}, nil
}
