//go:build linux

package computeruse

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// PlatformStatus reports the compiled native automation backend.
func PlatformStatus() PlatformReport {
	display := os.Getenv("DISPLAY")
	wmctrlPath, wmctrlErr := exec.LookPath("wmctrl")
	importPath, importErr := exec.LookPath("import")
	xdotoolPath, xdotoolErr := exec.LookPath("xdotool")
	gdbusPath, gdbusErr := exec.LookPath("gdbus")
	caps := []PlatformCapability{
		{Name: "intervention", Message: "Linux physical-intervention monitor is not implemented"},
	}
	if display != "" && wmctrlErr == nil && importErr == nil {
		caps = append(caps, PlatformCapability{Name: "app_state", Available: true, Message: "X11 window state and screenshots are available; AT-SPI enriches element trees when reachable"})
	} else {
		caps = append(caps, PlatformCapability{Name: "app_state", Message: "requires DISPLAY, wmctrl, and ImageMagick import"})
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
	dbus := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	noATBridge := strings.TrimSpace(os.Getenv("NO_AT_BRIDGE"))
	if dbus == "" {
		caps = append(caps, PlatformCapability{Name: "dbus", Message: "DBUS_SESSION_BUS_ADDRESS is not set"})
	} else {
		caps = append(caps, PlatformCapability{Name: "dbus", Available: true, Message: "DBUS session bus is set"})
	}
	switch {
	case dbus == "":
		caps = append(caps, PlatformCapability{Name: "atspi", Message: "DBUS_SESSION_BUS_ADDRESS is not set"})
	case noATBridge == "1" || strings.EqualFold(noATBridge, "true"):
		caps = append(caps, PlatformCapability{Name: "atspi", Message: "NO_AT_BRIDGE disables the AT-SPI bridge"})
	case gdbusErr != nil:
		caps = append(caps, PlatformCapability{Name: "atspi", Message: "gdbus is not available on PATH"})
	default:
		caps = append(caps, PlatformCapability{Name: "atspi", Available: true, Message: "DBUS session bus is set and gdbus is available at " + gdbusPath})
	}
	return PlatformReport{
		OS:           runtime.GOOS,
		Backend:      "linux-x11-partial",
		Capabilities: caps,
		Message:      "Linux native desktop automation has X11 state, screenshots, root-window input, a bounded AT-SPI reader, and AT-SPI element action dispatch; text/value element editing remains incomplete",
	}
}
