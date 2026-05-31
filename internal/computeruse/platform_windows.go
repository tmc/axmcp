//go:build windows

package computeruse

import (
	"os"
	"runtime"
)

// PlatformStatus reports the compiled native automation backend.
func PlatformStatus() PlatformReport {
	caps := []PlatformCapability{
		{Name: "app_state", Message: "UI Automation and Win32 window backend is not implemented"},
		{Name: "input", Message: "background Win32 or foreground SendInput backend is not implemented"},
		{Name: "screenshot", Message: "Windows Graphics Capture or GDI screenshot backend is not implemented"},
		{Name: "intervention", Message: "Windows physical-intervention monitor is not implemented"},
	}
	if os.Getenv("SESSIONNAME") == "" {
		caps = append(caps, PlatformCapability{Name: "session", Message: "SESSIONNAME is not set"})
	} else {
		caps = append(caps, PlatformCapability{Name: "session", Available: true, Message: "interactive session appears present"})
	}
	return PlatformReport{
		OS:           runtime.GOOS,
		Backend:      "windows-unsupported",
		Capabilities: caps,
		Message:      "Windows native desktop automation is not implemented",
	}
}
