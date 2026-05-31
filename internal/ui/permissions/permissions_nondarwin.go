//go:build !darwin

// Package permissions reports macOS permission status on Darwin and
// unavailable stubs elsewhere.
package permissions

import (
	"context"
	"fmt"
	"time"
)

type Requirement int

const (
	// ReqAccessibility is the macOS Accessibility permission.
	ReqAccessibility Requirement = iota
	// ReqScreenRecording is the macOS Screen Recording permission.
	ReqScreenRecording
)

// Status describes the current state of a permission requirement.
type Status int

const (
	// StatusUnknown means the permission has not been checked yet.
	StatusUnknown Status = iota
	// StatusGranted means the permission is currently available.
	StatusGranted
	// StatusDenied means a recent request did not grant the permission.
	StatusDenied
	// StatusMissing means the permission has not been granted or requested.
	StatusMissing
	// StatusStale means the recorded app identity no longer matches.
	StatusStale
	// StatusInProgress means a permission request flow is active.
	StatusInProgress
)

// Event reports a permission status transition from Watch.
type Event struct {
	Requirement Requirement
	Status      Status
	Detail      string
}

// Snapshot summarizes the current permission and identity state.
type Snapshot struct {
	AppName         string `json:"app_name,omitempty"`
	BundleID        string `json:"bundle_id,omitempty"`
	Accessibility   string `json:"accessibility"`
	ScreenRecording string `json:"screen_recording"`
	IdentityChanged bool   `json:"identity_changed"`
	IdentityDetail  string `json:"identity_detail,omitempty"`
	Pending         bool   `json:"pending"`
	Message         string `json:"message,omitempty"`
}

type AutomationOptions struct {
	AppName       string
	DismissAction string
	Timeout       time.Duration
	AutoPrompt    bool
	Remove        bool
	Reenable      bool
}

func unsupported() error {
	return fmt.Errorf("macOS permissions are not available on this platform")
}

// ConfigureIdentity is a no-op outside Darwin.
func ConfigureIdentity(string, string) {}

// Check reports missing for macOS-only permissions outside Darwin.
func Check(Requirement) Status {
	return StatusMissing
}

// Request reports that macOS permissions are unavailable.
func Request(context.Context, Requirement) (Status, error) {
	return StatusMissing, unsupported()
}

// Watch reports one missing event and then waits for cancellation.
func Watch(ctx context.Context, r Requirement, ch chan<- Event) {
	select {
	case ch <- Event{Requirement: r, Status: StatusMissing, Detail: "macOS permissions are not available on this platform"}:
	case <-ctx.Done():
		return
	}
	<-ctx.Done()
}

// ResetAndRetry reports that macOS permissions are unavailable.
func ResetAndRetry(Requirement) error {
	return unsupported()
}

// ResetIdentityState is a no-op outside Darwin.
func ResetIdentityState() {}

// OpenSystemSettings reports that macOS settings are unavailable.
func OpenSystemSettings(Requirement) error {
	return unsupported()
}

// CurrentSnapshot returns a missing-permissions snapshot.
func CurrentSnapshot(...Requirement) Snapshot {
	return Snapshot{
		Accessibility:   "missing",
		ScreenRecording: "missing",
		Pending:         true,
		Message:         "macOS permissions are not available on this platform",
	}
}

// OnboardingWindow reports that macOS permissions are unavailable.
func OnboardingWindow(context.Context, ...Requirement) error {
	return unsupported()
}

// Automate reports that macOS permissions are unavailable.
func Automate(context.Context, AutomationOptions, ...Requirement) error {
	return unsupported()
}

// DismissPrompt reports that macOS permission prompts are unavailable.
func DismissPrompt(context.Context, string, string) (bool, error) {
	return false, unsupported()
}

// ReenableApp reports that macOS settings automation is unavailable.
func ReenableApp(context.Context, Requirement, string) error {
	return unsupported()
}

// SetAppPermission reports that macOS settings automation is unavailable.
func SetAppPermission(context.Context, Requirement, string, bool) error {
	return unsupported()
}

// RemoveApp reports that macOS settings automation is unavailable.
func RemoveApp(context.Context, Requirement, string) error {
	return unsupported()
}
