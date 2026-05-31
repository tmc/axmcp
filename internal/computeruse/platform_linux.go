//go:build linux

package computeruse

import (
	"os"
	"os/exec"
	"runtime"
)

// PlatformStatus reports the compiled native automation backend.
func PlatformStatus() PlatformReport {
	display := os.Getenv("DISPLAY")
	wmctrlPath, wmctrlErr := exec.LookPath("wmctrl")
	importPath, importErr := exec.LookPath("import")
	xdotoolPath, xdotoolErr := exec.LookPath("xdotool")
	caps := []PlatformCapability{
		{Name: "app_state", Message: "AT-SPI accessibility tree backend is not implemented"},
		{Name: "intervention", Message: "Linux physical-intervention monitor is not implemented"},
	}
	switch {
	case display == "":
		caps = append(caps, PlatformCapability{Name: "x11_window_enumeration", Message: "DISPLAY is not set"})
	case wmctrlErr != nil:
		caps = append(caps, PlatformCapability{Name: "x11_window_enumeration", Message: "wmctrl is not available on PATH"})
	default:
		caps = append(caps, PlatformCapability{Name: "x11_window_enumeration", Available: true, Message: "wmctrl-backed X11 top-level window enumeration is available at " + wmctrlPath})
	}
	switch {
	case display == "":
		caps = append(caps, PlatformCapability{Name: "screenshot", Message: "DISPLAY is not set"})
	case importErr != nil:
		caps = append(caps, PlatformCapability{Name: "screenshot", Message: "ImageMagick import is not available on PATH"})
	default:
		caps = append(caps, PlatformCapability{Name: "screenshot", Available: true, Message: "ImageMagick import-backed X11 screenshot is available at " + importPath})
	}
	switch {
	case display == "":
		caps = append(caps, PlatformCapability{Name: "input", Message: "DISPLAY is not set"})
	case xdotoolErr != nil:
		caps = append(caps, PlatformCapability{Name: "input", Message: "xdotool is not available on PATH"})
	default:
		caps = append(caps, PlatformCapability{Name: "input", Available: true, Message: "xdotool-backed X11 root-window pixel and key input is available at " + xdotoolPath})
	}
	if display == "" {
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
		Message:      "Linux native desktop automation is partially scaffolded; AT-SPI element actions are not implemented",
	}
}
