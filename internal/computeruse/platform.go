package computeruse

import (
	"errors"
	"fmt"
	"strings"
)

// PlatformCapability describes one native automation capability for the
// current platform backend.
type PlatformCapability struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Message   string `json:"message,omitempty"`
}

// PlatformReport describes native desktop automation support for this build.
type PlatformReport struct {
	OS                      string               `json:"os"`
	Backend                 string               `json:"backend"`
	NativeDesktopAutomation bool                 `json:"native_desktop_automation"`
	Capabilities            []PlatformCapability `json:"capabilities,omitempty"`
	Message                 string               `json:"message,omitempty"`
}

// ErrPlatformUnsupported reports that native desktop automation is not
// implemented for the current operating system.
var ErrPlatformUnsupported = errors.New("computer-use native desktop automation is not supported on this platform")

// PlatformUnsupported wraps ErrPlatformUnsupported with the unavailable
// feature name.
func PlatformUnsupported(feature string) error {
	feature = strings.TrimSpace(feature)
	if feature == "" {
		return ErrPlatformUnsupported
	}
	return fmt.Errorf("%s: %w", feature, ErrPlatformUnsupported)
}
