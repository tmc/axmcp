//go:build !darwin

package axpump

import "fmt"

// Ensure reports that macOS Accessibility pumping is unavailable.
func Ensure(pid int32) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("invalid pid %d", pid)
	}
	return false, fmt.Errorf("accessibility pump is not available on this platform")
}
