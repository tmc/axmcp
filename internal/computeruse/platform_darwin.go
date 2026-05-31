//go:build darwin

package computeruse

import "runtime"

// PlatformStatus reports the compiled native automation backend.
func PlatformStatus() PlatformReport {
	return PlatformReport{
		OS:                      runtime.GOOS,
		Backend:                 "darwin-accessibility",
		NativeDesktopAutomation: true,
		Capabilities: []PlatformCapability{
			{Name: "app_state", Available: true, Message: "implemented with macOS Accessibility and screen capture APIs"},
			{Name: "input", Available: true, Message: "implemented with macOS Accessibility, CoreGraphics, and SkyLight input paths"},
			{Name: "intervention", Available: true, Message: "implemented with macOS event monitoring"},
			{Name: "session", Available: true, Message: "implemented with in-process state snapshots"},
		},
	}
}
