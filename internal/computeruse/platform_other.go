//go:build !darwin && !linux && !windows

package computeruse

import "runtime"

// PlatformStatus reports the compiled native automation backend.
func PlatformStatus() PlatformReport {
	return PlatformReport{
		OS:      runtime.GOOS,
		Backend: "unsupported",
		Capabilities: []PlatformCapability{
			{Name: "app_state", Message: "no native app-state backend is implemented for this platform"},
			{Name: "input", Message: "no native input backend is implemented for this platform"},
			{Name: "screenshot", Message: "no native screenshot backend is implemented for this platform"},
			{Name: "intervention", Message: "no physical-intervention monitor is implemented for this platform"},
		},
		Message: "native desktop automation is not implemented for this platform",
	}
}
