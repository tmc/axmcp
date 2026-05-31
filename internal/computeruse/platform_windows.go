//go:build windows

package computeruse

import (
	"os"
	"runtime"
)

// PlatformStatus reports the compiled native automation backend.
func PlatformStatus() PlatformReport {
	caps := []PlatformCapability{
		{Name: "win32_window_enumeration", Available: true, Message: "compiled Win32 top-level window enumeration backend"},
		{Name: "app_state", Available: true, Message: "state uses Win32 windows, screenshots, and a bounded UIA control-view reader with root fallback"},
		{Name: "input", Available: true, Message: "pixel clicks, drags, key presses, and text use background Win32 messages; UIA invoke/toggle/select/expand-collapse/set-value patterns are available; foreground SendInput is not implemented"},
		{Name: "screenshot", Available: true, Message: "state screenshots use PrintWindow with a GDI BitBlt fallback; WGC is not implemented"},
		{Name: "intervention", Message: "Windows physical-intervention monitor is not implemented"},
	}
	if os.Getenv("SESSIONNAME") == "" {
		caps = append(caps, PlatformCapability{Name: "session", Message: "SESSIONNAME is not set"})
	} else {
		caps = append(caps, PlatformCapability{Name: "session", Available: true, Message: "interactive session appears present"})
	}
	return PlatformReport{
		OS:           runtime.GOOS,
		Backend:      "windows-win32-partial",
		Capabilities: caps,
		Message:      "Windows native desktop automation is partially implemented",
	}
}
