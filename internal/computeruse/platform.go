package computeruse

import (
	"errors"
	"fmt"
	"strings"
)

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
