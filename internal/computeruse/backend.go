package computeruse

import (
	"context"
)

// Backend groups the native operations needed by cmd/computer-use-mcp.
// Platform implementations should preserve the public state_id contract:
// callers receive portable AppState data, while any native element handles
// remain inside the returned Snapshot.
type Backend interface {
	Platform() PlatformReport
	State() StateBackend
	Input() InputBackend
	Screenshots() ScreenshotBackend
	Intervention() InterventionBackend
}

// StateRequest describes one app-state capture.
type StateRequest struct {
	App          string
	WindowTitle  string
	Instructions InstructionProvider
}

// Snapshot is a live native state handle stored behind a state_id.
type Snapshot interface {
	State() AppState
	Close() error
}

// StateBackend resolves apps and builds live state snapshots.
type StateBackend interface {
	ListApps(context.Context) ([]AppInfo, error)
	ResolveApp(context.Context, string) (AppInfo, error)
	BuildState(context.Context, StateRequest) (Snapshot, error)
}

// Point is a pixel coordinate in the returned screenshot space.
type Point struct {
	X int
	Y int
}

// ClickOptions controls a click action.
type ClickOptions struct {
	Button        string
	ClickCount    int
	ForegroundHID bool
}

// DragOptions controls a drag action.
type DragOptions struct {
	Button string
}

// ScrollOptions controls a scroll action.
type ScrollOptions struct {
	Direction string
	Pages     float64
}

// InputBackend performs actions against a live Snapshot.
type InputBackend interface {
	ClickElement(context.Context, Snapshot, int, ClickOptions) error
	ClickPoint(context.Context, Snapshot, Point, ClickOptions) error
	Drag(context.Context, Snapshot, Point, Point, DragOptions) error
	ScrollElement(context.Context, Snapshot, int, ScrollOptions) error
	PerformSecondaryAction(context.Context, Snapshot, int, string) error
	SetValue(context.Context, Snapshot, int, string) error
	PressKey(context.Context, Snapshot, string) error
	TypeText(context.Context, Snapshot, *int, string) error
}

// ScreenshotRequest identifies a window screenshot to capture.
type ScreenshotRequest struct {
	App       AppInfo
	Window    WindowInfo
	MaxWidth  int
	MaxHeight int
}

// Screenshot reports PNG data and the pixel coordinate frame it uses.
type Screenshot struct {
	PNG    []byte
	Width  int
	Height int
}

// ScreenshotBackend captures pixels for a target window.
type ScreenshotBackend interface {
	CaptureWindow(context.Context, ScreenshotRequest) (Screenshot, error)
}

// InterventionStatus describes whether recent user input should block actions.
type InterventionStatus struct {
	Enabled     bool
	Blocked     bool
	Message     string
	LastType    string
	LastKind    string
	LastPID     int64
	QuietMillis int64
}

// InterventionBackend monitors physical user input.
type InterventionBackend interface {
	Start() error
	Close() error
	Status(context.Context) (InterventionStatus, error)
}

// UnsupportedBackend returns ErrPlatformUnsupported for all native operations.
type UnsupportedBackend struct {
	Report PlatformReport
}

// NewUnsupportedBackend returns a backend with the supplied platform report.
func NewUnsupportedBackend(report PlatformReport) *UnsupportedBackend {
	return &UnsupportedBackend{Report: report}
}

func (b *UnsupportedBackend) Platform() PlatformReport {
	if b == nil {
		return PlatformReport{Message: ErrPlatformUnsupported.Error()}
	}
	return b.Report
}

func (b *UnsupportedBackend) State() StateBackend            { return unsupportedStateBackend{} }
func (b *UnsupportedBackend) Input() InputBackend            { return unsupportedInputBackend{} }
func (b *UnsupportedBackend) Screenshots() ScreenshotBackend { return unsupportedScreenshotBackend{} }
func (b *UnsupportedBackend) Intervention() InterventionBackend {
	return unsupportedInterventionBackend{}
}

type unsupportedStateBackend struct{}

func (unsupportedStateBackend) ListApps(context.Context) ([]AppInfo, error) {
	return nil, PlatformUnsupported("list apps")
}

func (unsupportedStateBackend) ResolveApp(context.Context, string) (AppInfo, error) {
	return AppInfo{}, PlatformUnsupported("resolve app")
}

func (unsupportedStateBackend) BuildState(context.Context, StateRequest) (Snapshot, error) {
	return nil, PlatformUnsupported("build app state")
}

type unsupportedInputBackend struct{}

func (unsupportedInputBackend) ClickElement(context.Context, Snapshot, int, ClickOptions) error {
	return PlatformUnsupported("click element")
}

func (unsupportedInputBackend) ClickPoint(context.Context, Snapshot, Point, ClickOptions) error {
	return PlatformUnsupported("click point")
}

func (unsupportedInputBackend) Drag(context.Context, Snapshot, Point, Point, DragOptions) error {
	return PlatformUnsupported("drag")
}

func (unsupportedInputBackend) ScrollElement(context.Context, Snapshot, int, ScrollOptions) error {
	return PlatformUnsupported("scroll element")
}

func (unsupportedInputBackend) PerformSecondaryAction(context.Context, Snapshot, int, string) error {
	return PlatformUnsupported("perform secondary action")
}

func (unsupportedInputBackend) SetValue(context.Context, Snapshot, int, string) error {
	return PlatformUnsupported("set value")
}

func (unsupportedInputBackend) PressKey(context.Context, Snapshot, string) error {
	return PlatformUnsupported("press key")
}

func (unsupportedInputBackend) TypeText(context.Context, Snapshot, *int, string) error {
	return PlatformUnsupported("type text")
}

type unsupportedScreenshotBackend struct{}

func (unsupportedScreenshotBackend) CaptureWindow(context.Context, ScreenshotRequest) (Screenshot, error) {
	return Screenshot{}, PlatformUnsupported("capture window screenshot")
}

type unsupportedInterventionBackend struct{}

func (unsupportedInterventionBackend) Start() error {
	return PlatformUnsupported("human intervention monitor")
}
func (unsupportedInterventionBackend) Close() error { return nil }
func (unsupportedInterventionBackend) Status(context.Context) (InterventionStatus, error) {
	return InterventionStatus{}, PlatformUnsupported("human intervention monitor")
}
