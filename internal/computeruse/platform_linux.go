//go:build linux

package computeruse

import (
	"os"
	"runtime"
)

// PlatformStatus reports the compiled native automation backend.
func PlatformStatus() PlatformReport {
	caps := []PlatformCapability{
		{Name: "app_state", Message: "AT-SPI app and accessibility tree backend is not implemented"},
		{Name: "input", Message: "X11, Wayland, or portal input backend is not implemented"},
		{Name: "screenshot", Message: "X11, Wayland, or portal screenshot backend is not implemented"},
		{Name: "intervention", Message: "Linux physical-intervention monitor is not implemented"},
	}
	if os.Getenv("DISPLAY") == "" {
		caps = append(caps, PlatformCapability{Name: "x11", Message: "DISPLAY is not set"})
	} else {
		caps = append(caps, PlatformCapability{Name: "x11", Available: true, Message: "DISPLAY is set"})
	}
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		caps = append(caps, PlatformCapability{Name: "wayland", Message: "WAYLAND_DISPLAY is not set"})
	} else {
		caps = append(caps, PlatformCapability{Name: "wayland", Available: true, Message: "WAYLAND_DISPLAY is set"})
	}
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		caps = append(caps, PlatformCapability{Name: "dbus", Message: "DBUS_SESSION_BUS_ADDRESS is not set"})
	} else {
		caps = append(caps, PlatformCapability{Name: "dbus", Available: true, Message: "DBUS session bus is set"})
	}
	return PlatformReport{
		OS:           runtime.GOOS,
		Backend:      "linux-unsupported",
		Capabilities: caps,
		Message:      "Linux native desktop automation is not implemented",
	}
}
