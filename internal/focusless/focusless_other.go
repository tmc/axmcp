//go:build !darwin

package focusless

import "errors"

// ErrUnavailable is returned by every function on non-darwin platforms.
var ErrUnavailable = errors.New("focusless: SkyLight unavailable")

// Snapshot is the observable focus state at one instant.
type Snapshot struct {
	FrontPID     int    `json:"front_pid"`
	WorkspacePID int    `json:"workspace_pid"`
	ActiveSpace  uint64 `json:"active_space"`
}

// Capture always fails on non-darwin platforms.
func Capture() (Snapshot, error) { return Snapshot{}, ErrUnavailable }

// FocusedWindow always reports none on non-darwin platforms.
func FocusedWindow(pid int) uint32 { return 0 }

// IsOffSpace always fails on non-darwin platforms.
func IsOffSpace(wid uint32) (bool, error) { return false, ErrUnavailable }
