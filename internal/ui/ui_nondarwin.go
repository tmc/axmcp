//go:build !darwin

// Package ui provides macOS UI automation on Darwin and unavailable stubs
// elsewhere.
package ui

import (
	"fmt"
	"time"
)

// Device is unavailable outside Darwin.
type Device struct{}

func SharedDevice() *Device            { return &Device{} }
func (d *Device) PressHome()           {}
func (d *Device) PressVolumeUp()       {}
func (d *Device) PressVolumeDown()     {}
func (d *Device) PressLock()           {}
func (d *Device) SetOrientation(o int) {}

// App represents a target application.
type App struct {
	bundleID string
}

// Element represents a target UI element.
type Element struct{}

// Attributes describes an element for inspection.
type Attributes struct {
	Label      string
	Identifier string
	Title      string
	Value      string
	Enabled    bool
	Selected   bool
	HasFocus   bool
}

type QueryParams struct {
	Role       string
	Title      string
	Identifier string
	Label      string
}

type PermissionStatus string

const (
	PermissionStatusGranted    PermissionStatus = "granted"
	PermissionStatusMissing    PermissionStatus = "missing"
	PermissionStatusInProgress PermissionStatus = "in_progress"
)

type PermissionSnapshot struct {
	Accessibility   PermissionStatus
	ScreenRecording PermissionStatus
	Pending         bool
	Message         string
}

func unsupported() error {
	return fmt.Errorf("macOS UI automation is not available on this platform")
}

func MkString(string) uintptr { return 0 }

func WaitForWindows() {}

func ConfigureIdentity(string, string) {}

func RequestAccessibilityPermission() {}

func PrivacySettingsURL(service string) string {
	switch service {
	case "Accessibility":
		return "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
	case "ScreenCapture":
		return "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"
	default:
		return "x-apple.systempreferences:com.apple.preference.security"
	}
}

func IsTrusted() bool { return false }

func CheckTrust() {}

func WaitForAccessibility(time.Duration) bool { return false }

func IsScreenRecordingTrusted() bool { return false }

func ScreenCaptureTerminateGuardActive() bool { return false }

func RequestScreenCapturePermission() {}

func ResetTCC(string) {}

func WaitForScreenRecording(time.Duration) bool { return false }

func CheckScreenCapture() {}

func NewApp(bundleID string) *App {
	return &App{bundleID: bundleID}
}

func ApplicationWithBundleID(bundleID string) *App {
	return NewApp(bundleID)
}

func Application() *App {
	return NewApp("com.apple.iphonesimulator")
}

func (a *App) Exists() bool { return false }
func (a *App) Terminate()   {}
func (a *App) Activate()    {}
func (a *App) Launch()      {}
func (a *App) Element() *Element {
	return &Element{}
}
func (a *App) Tree() string { return "" }

func ElementByID(string) *Element { return nil }

func (e *Element) ElementByID(string) *Element { return nil }
func (e *Element) Tap()                        {}
func (e *Element) PerformAction(string)        {}
func (e *Element) Exists() bool                { return false }
func (e *Element) Tree() string                { return "" }
func (e *Element) VisualTree() string          { return "" }
func (e *Element) Role() string                { return "" }
func (e *Element) Title() string               { return "" }
func (e *Element) Label() string               { return "" }
func (e *Element) Identifier() string          { return "" }
func (e *Element) Attributes() Attributes      { return Attributes{} }
func (e *Element) Screenshot() ([]byte, error) { return nil, unsupported() }
func (e *Element) Children() []*Element        { return nil }
func (e *Element) FindChildren(string) []*Element {
	return nil
}
func (e *Element) Windows() []*Element { return nil }
func (e *Element) Buttons() []*Element { return nil }
func (e *Element) Query(QueryParams) []*Element {
	return nil
}

func BytePtrToString(uintptr) string { return "" }

func CurrentPermissionSnapshot() PermissionSnapshot {
	return PermissionSnapshot{
		Accessibility:   PermissionStatusMissing,
		ScreenRecording: PermissionStatusMissing,
		Pending:         true,
		Message:         "macOS permissions are not available on this platform",
	}
}
